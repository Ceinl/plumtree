// Package v1 implements the authenticated control and artifact API mounted on
// the private SSH control transport.
package v1

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Ceinl/plumtree/internal/protocol/control"
	"github.com/Ceinl/plumtree/internal/server/identity"
	"github.com/Ceinl/plumtree/internal/sqlite"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
)

const (
	APIVersion           = 1
	DefaultArtifactBytes = 32 << 20
	DefaultMetadataBytes = 64 << 10
	MultipartOverhead    = 16 << 10
	ProductVersionHeader = control.VersionHeader
)

var (
	errUnauthorized = errors.New("api: unauthorized")
	errForbidden    = errors.New("api: forbidden")
	errVersion      = errors.New("api: product version mismatch")
	errMalformed    = errors.New("api: malformed request")
	errMetadata     = errors.New("api: invalid metadata")
	errMediaType    = errors.New("api: unsupported media type")
	errTooLarge     = errors.New("api: request too large")
	errABI          = errors.New("api: unsupported ABI")
	errArtifact     = errors.New("api: invalid artifact")
)

type principalKey struct{}

// Principal is the already-authenticated device identity supplied by the
// private SSH control transport. Author/device fields in HTTP bodies are never
// used for authorization.
type Principal struct {
	ServerID, AuthorID, DeviceID, Fingerprint string
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type ArtifactValidator func(context.Context, []byte, int) error

type Config struct {
	Repository       *sqlite.Repository
	Identity         *identity.Service
	ProductVersion   string
	ABIVersion       uint8
	ArtifactLimit    int64
	MetadataLimit    int64
	ValidateArtifact ArtifactValidator
}

type Server struct {
	repo             *sqlite.Repository
	identity         *identity.Service
	product          string
	abi              uint8
	artifactMax      int64
	metadataMax      int64
	validateArtifact ArtifactValidator
}

func New(cfg Config) (*Server, error) {
	if cfg.Repository == nil || cfg.Identity == nil {
		return nil, fmt.Errorf("%w: repository and identity service are required", errForbidden)
	}
	if strings.TrimSpace(cfg.ProductVersion) == "" {
		return nil, fmt.Errorf("%w: product version is required", errVersion)
	}
	artifactMax, metadataMax := cfg.ArtifactLimit, cfg.MetadataLimit
	if artifactMax == 0 {
		artifactMax = DefaultArtifactBytes
	}
	if metadataMax == 0 {
		metadataMax = DefaultMetadataBytes
	}
	if artifactMax < 1 || metadataMax < 1 {
		return nil, fmt.Errorf("%w: limits must be positive", errMalformed)
	}
	abiVersion := cfg.ABIVersion
	if abiVersion == 0 {
		abiVersion = abi.Version
	}
	return &Server{repo: cfg.Repository, identity: cfg.Identity, product: cfg.ProductVersion, abi: abiVersion, artifactMax: artifactMax, metadataMax: metadataMax, validateArtifact: cfg.ValidateArtifact}, nil
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || !strings.HasPrefix(r.URL.Path, control.APIPrefix) ||
		(r.URL.Path != control.APIPrefix && !strings.HasPrefix(r.URL.Path, control.APIPrefix+"/")) {
		s.problem(w, problem{status: http.StatusNotFound, code: "not_found", title: "Not Found", detail: "API route not found"})
		return
	}
	if err := control.ValidateRequest(r); err != nil {
		s.problem(w, problem{status: http.StatusBadRequest, code: "malformed_request", title: "Malformed request", detail: "request path or headers are invalid"})
		return
	}
	if r.URL.Path == control.APIPrefix+"/version" {
		s.handleVersion(w, r)
		return
	}
	if actual := r.Header.Get(ProductVersionHeader); actual != s.product {
		s.problem(w, problem{status: http.StatusConflict, code: "product_version_mismatch", title: "Product version mismatch", detail: "pt and server must use the same Plumtree product version", expected: s.product, actual: actual})
		return
	}
	principal, err := s.authorize(r.Context())
	if err != nil {
		s.problemForError(w, err)
		return
	}
	segments, ok := routeSegments(r.URL.Path)
	if !ok {
		s.problem(w, problem{status: http.StatusNotFound, code: "not_found", title: "Not Found", detail: "API route not found"})
		return
	}
	s.route(w, r, principal, segments)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.problem(w, problem{status: http.StatusMethodNotAllowed, code: "method_not_allowed", title: "Method not allowed", detail: "use GET for the version endpoint"})
		return
	}
	if _, err := s.authorize(r.Context()); err != nil {
		s.problemForError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"product": "plumtree", "version": s.product, "apiVersion": APIVersion,
		"abiVersion": s.abi, "limits": map[string]int64{"artifactBytes": s.artifactMax, "metadataBytes": s.metadataMax},
	})
}

func (s *Server) authorize(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok || p.ServerID == "" || p.AuthorID == "" || p.DeviceID == "" || p.Fingerprint == "" {
		return Principal{}, errUnauthorized
	}
	server, err := s.repo.ServerIdentity(ctx)
	if err != nil || server.ID != p.ServerID {
		return Principal{}, errUnauthorized
	}
	device, err := s.repo.Device(ctx, p.DeviceID)
	if err != nil || device.AuthorID != p.AuthorID || device.Fingerprint != p.Fingerprint || device.RevokedAt != nil {
		return Principal{}, errUnauthorized
	}
	author, err := s.repo.Author(ctx, p.AuthorID)
	if err != nil {
		return Principal{}, errUnauthorized
	}
	if author.Suspended {
		return Principal{}, errForbidden
	}
	return p, nil
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, p Principal, parts []string) {
	if len(parts) == 1 && parts[0] == "deployments" {
		if r.Method == http.MethodPost {
			s.handleDeployment(w, r, p, "")
			return
		}
	}
	if len(parts) == 2 && parts[0] == "deployments" && r.Method == http.MethodPut {
		s.handleDeployment(w, r, p, parts[1])
		return
	}
	if len(parts) == 1 && parts[0] == "author" && r.Method == http.MethodGet {
		author, err := s.repo.Author(r.Context(), p.AuthorID)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"author": author})
		return
	}
	if len(parts) == 1 && parts[0] == "devices" && r.Method == http.MethodGet {
		devices, err := s.identity.Devices(r.Context(), p.AuthorID)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
		return
	}
	if len(parts) == 1 && parts[0] == "devices" && r.Method == http.MethodPost {
		var req struct {
			DeviceName string `json:"deviceName"`
		}
		if !s.readJSON(w, r, &req) {
			return
		}
		challenge, err := s.identity.BeginDeviceAddition(r.Context(), p.AuthorID, p.DeviceID, req.DeviceName)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, map[string]any{"invitation": map[string]any{"id": challenge.ID, "secret": string(challenge.Secret), "deviceName": challenge.DeviceName, "expiresAt": challenge.ExpiresAt}})
		return
	}
	if len(parts) == 2 && parts[0] == "devices" && r.Method == http.MethodDelete {
		if err := s.identity.RevokeDevice(r.Context(), p.AuthorID, p.DeviceID, parts[1]); err != nil {
			s.problemForError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 1 && parts[0] == "audit" && r.Method == http.MethodGet {
		limit := 100
		events, err := s.repo.ListAudit(r.Context(), p.AuthorID, limit)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"events": events})
		return
	}
	if len(parts) >= 1 && parts[0] == "apps" {
		s.routeApps(w, r, p, parts[1:])
		return
	}
	s.problem(w, problem{status: http.StatusNotFound, code: "not_found", title: "Not Found", detail: "API route not found"})
}

func (s *Server) routeApps(w http.ResponseWriter, r *http.Request, p Principal, parts []string) {
	if len(parts) == 0 {
		if r.Method == http.MethodGet {
			apps, err := s.repo.ListApps(r.Context(), p.AuthorID)
			if err != nil {
				s.problemForError(w, err)
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
			return
		}
		if r.Method == http.MethodPost {
			var req struct{ Name, Kind, AccessMode string }
			if !s.readJSON(w, r, &req) {
				return
			}
			appID, err := newID("app")
			if err != nil {
				s.problemForError(w, err)
				return
			}
			app, err := s.repo.CreateApp(r.Context(), sqlite.AppInput{ID: appID, AuthorID: p.AuthorID, CreatedByDeviceID: p.DeviceID, Name: req.Name, Kind: req.Kind, AccessMode: req.AccessMode})
			if err != nil {
				s.problemForError(w, err)
				return
			}
			s.writeJSON(w, http.StatusCreated, map[string]any{"app": app})
			return
		}
	}
	if len(parts) == 1 {
		app, ok := s.ownedApp(r.Context(), p, parts[0])
		if !ok {
			s.problemForError(w, sqlite.ErrNotFound)
			return
		}
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w)
			return
		}
		current, artifact, currentErr := s.repo.CurrentDeployment(r.Context(), app.ID)
		response := map[string]any{"app": app}
		if currentErr == nil {
			response["activeDeployment"] = map[string]any{"id": current.ID, "artifactId": current.ArtifactID, "artifact": artifact}
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	app, ok := s.ownedApp(r.Context(), p, parts[0])
	if !ok {
		s.problemForError(w, sqlite.ErrNotFound)
		return
	}
	if len(parts) == 2 && parts[1] == "access" {
		s.handleAccess(w, r, p, app.ID, "")
		return
	}
	if len(parts) == 3 && parts[1] == "access" {
		s.handleAccess(w, r, p, app.ID, parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "secrets" {
		s.handleSecrets(w, r, p, app.ID, "")
		return
	}
	if len(parts) == 3 && parts[1] == "secrets" {
		s.handleSecrets(w, r, p, app.ID, parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "egress" {
		s.handleEgress(w, r, app.ID, "")
		return
	}
	if len(parts) == 3 && parts[1] == "egress" {
		s.handleEgress(w, r, app.ID, parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "sessions" && r.Method == http.MethodGet {
		sessions, err := s.repo.ListSessions(r.Context(), app.ID, 100)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
		return
	}
	s.problem(w, problem{status: http.StatusNotFound, code: "not_found", title: "Not Found", detail: "app route not found"})
}

func (s *Server) ownedApp(ctx context.Context, p Principal, appID string) (sqlite.App, bool) {
	app, err := s.repo.App(ctx, appID)
	return app, err == nil && app.AuthorID == p.AuthorID
}

func (s *Server) handleAccess(w http.ResponseWriter, r *http.Request, p Principal, appID, keyID string) {
	switch r.Method {
	case http.MethodGet:
		if keyID != "" {
			s.problemForError(w, sqlite.ErrNotFound)
			return
		}
		keys, err := s.repo.ListAccessKeys(r.Context(), p.AuthorID, appID)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
	case http.MethodPost:
		var req struct{ Name, PublicKey, Fingerprint string }
		if !s.readJSON(w, r, &req) {
			return
		}
		keyID, err := newID("access")
		if err != nil {
			s.problemForError(w, err)
			return
		}
		key, err := s.repo.AddAccessKey(r.Context(), sqlite.AccessKeyInput{ID: keyID, AppID: appID, Name: req.Name, PublicKey: req.PublicKey, Fingerprint: req.Fingerprint, AddedByDeviceID: p.DeviceID})
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, map[string]any{"key": key})
	case http.MethodDelete:
		if keyID == "" {
			s.problemForError(w, errMalformed)
			return
		}
		if err := s.repo.RemoveAccessKey(r.Context(), p.AuthorID, appID, keyID, p.DeviceID); err != nil {
			s.problemForError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request, p Principal, appID, key string) {
	switch r.Method {
	case http.MethodGet:
		if key != "" {
			s.problemForError(w, sqlite.ErrNotFound)
			return
		}
		items, err := s.repo.ListSecrets(r.Context(), appID)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"secrets": items})
	case http.MethodPut, http.MethodPost:
		if key == "" {
			s.problemForError(w, errMalformed)
			return
		}
		var req struct{ Value string }
		if !s.readJSON(w, r, &req) {
			return
		}
		if len(req.Value) > 128<<10 {
			s.problem(w, problem{status: http.StatusRequestEntityTooLarge, code: "request_too_large", title: "Request too large", detail: "secret value exceeds the limit", limit: 128 << 10})
			return
		}
		meta, err := s.repo.SetSecret(r.Context(), appID, key, []byte(req.Value))
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"key": meta.Key, "version": meta.Version, "updatedAt": meta.UpdatedAt})
	case http.MethodDelete:
		if key == "" {
			s.problemForError(w, errMalformed)
			return
		}
		if err := s.repo.SetSecretDelete(r.Context(), appID, key); err != nil {
			s.problemForError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) handleEgress(w http.ResponseWriter, r *http.Request, appID, host string) {
	switch r.Method {
	case http.MethodGet:
		if host != "" {
			s.problemForError(w, sqlite.ErrNotFound)
			return
		}
		hosts, err := s.repo.ListEgressHosts(r.Context(), appID)
		if err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
	case http.MethodPut, http.MethodPost:
		if host == "" {
			var req struct{ Host string }
			if !s.readJSON(w, r, &req) {
				return
			}
			host = req.Host
		}
		if err := s.repo.SetEgressHost(r.Context(), appID, host, true); err != nil {
			s.problemForError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"host": host, "allowed": true})
	case http.MethodDelete:
		if host == "" {
			s.problemForError(w, errMalformed)
			return
		}
		if err := s.repo.SetEgressHost(r.Context(), appID, host, false); err != nil {
			s.problemForError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w)
	}
}

func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request, p Principal, previous string) {
	metadata, wasm, err := s.readMultipart(w, r)
	if err != nil {
		s.problemForError(w, err)
		return
	}
	if metadata.ABIVersion != s.abi {
		s.problem(w, problem{status: http.StatusUnprocessableEntity, code: "abi_unsupported", title: "Unsupported ABI", detail: "artifact ABI is not supported by this server", expected: fmt.Sprint(s.abi), actual: fmt.Sprint(metadata.ABIVersion)})
		return
	}
	if err := s.validateWASM(r.Context(), wasm, metadata.ABIVersion); err != nil {
		s.problemForError(w, err)
		return
	}
	artifactID, err := newID("artifact")
	if err != nil {
		s.problemForError(w, err)
		return
	}
	artifactInput := sqlite.ArtifactInput{ID: artifactID, Digest: digestBytes(wasm), WASM: wasm, ABIVersion: int(metadata.ABIVersion), BuildMetadata: metadata.BuildMetadata}
	result, err := s.repo.DeployApplication(r.Context(), sqlite.ApplicationDeploymentInput{AuthorID: p.AuthorID, DeviceID: p.DeviceID, AppName: metadata.AppName, Kind: metadata.AppType, AccessMode: metadata.AccessMode, SourceDigest: metadata.SourceDigest, PreviousDeploymentID: previous, Artifact: artifactInput})
	if err != nil {
		s.problemForError(w, err)
		return
	}
	author, err := s.repo.Author(r.Context(), p.AuthorID)
	if err != nil {
		s.problemForError(w, err)
		return
	}
	status := http.StatusCreated
	if previous != "" {
		status = http.StatusOK
	}
	s.writeJSON(w, status, map[string]any{"apiVersion": APIVersion,
		"app":      map[string]any{"id": result.App.ID, "name": result.App.Name, "handle": author.Handle + "/" + result.App.Name, "activeDeployId": result.Deployment.ID},
		"deploy":   map[string]any{"id": result.Deployment.ID, "createdAt": result.Deployment.CreatedAt},
		"artifact": result.Artifact})
}

type deploymentMetadata struct {
	AppName       string            `json:"appName"`
	AppType       string            `json:"appType"`
	AccessMode    string            `json:"accessMode"`
	ABIVersion    uint8             `json:"abiVersion"`
	SourceDigest  string            `json:"sourceDigest"`
	BuildMetadata map[string]string `json:"buildMetadata"`
}

func (s *Server) readMultipart(w http.ResponseWriter, r *http.Request) (deploymentMetadata, []byte, error) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return deploymentMetadata{}, nil, errMediaType
	}
	if enc := strings.TrimSpace(r.Header.Get("Content-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
		return deploymentMetadata{}, nil, errMediaType
	}
	totalLimit := s.artifactMax + s.metadataMax + MultipartOverhead
	if r.ContentLength > totalLimit {
		return deploymentMetadata{}, nil, fmt.Errorf("%w: total multipart body", errTooLarge)
	}
	r.Body = http.MaxBytesReader(w, r.Body, totalLimit)
	mr, err := r.MultipartReader()
	if err != nil {
		return deploymentMetadata{}, nil, errMalformed
	}
	part, err := mr.NextPart()
	if err != nil {
		return deploymentMetadata{}, nil, errMalformed
	}
	if part.FormName() != "metadata" || part.FileName() != "" || part.Header.Get("Content-Type") != "application/json" {
		return deploymentMetadata{}, nil, errMalformed
	}
	metadataBytes, err := readBounded(part, s.metadataMax)
	if err != nil {
		return deploymentMetadata{}, nil, err
	}
	var metadata deploymentMetadata
	dec := json.NewDecoder(strings.NewReader(string(metadataBytes)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&metadata); err != nil {
		return deploymentMetadata{}, nil, errMetadata
	}
	if err := ensureSingleJSON(dec); err != nil {
		return deploymentMetadata{}, nil, errMetadata
	}
	part, err = mr.NextPart()
	if err != nil {
		return deploymentMetadata{}, nil, errMalformed
	}
	if part.FormName() != "artifact" || part.Header.Get("Content-Type") != "application/wasm" {
		return deploymentMetadata{}, nil, errMalformed
	}
	wasm, err := readBounded(part, s.artifactMax)
	if err != nil {
		return deploymentMetadata{}, nil, err
	}
	if part, err = mr.NextPart(); err != io.EOF {
		return deploymentMetadata{}, nil, errMalformed
	}
	if err := validateMetadata(metadata, s.abi); err != nil {
		return deploymentMetadata{}, nil, err
	}
	return metadata, wasm, nil
}

func validateMetadata(m deploymentMetadata, abiVersion uint8) error {
	if m.AppName == "" || len(m.AppName) > 128 || strings.ContainsAny(m.AppName, "/\\ \t\r\n") || (m.AppType != "tui" && m.AppType != "cli") || (m.AccessMode != "public" && m.AccessMode != "restricted") || !validDigest(m.SourceDigest) {
		return errMetadata
	}
	if m.ABIVersion != abiVersion {
		return errABI
	}
	if len(m.BuildMetadata) > 64 {
		return errMetadata
	}
	for key, value := range m.BuildMetadata {
		if key == "" || len(key) > 128 || len(value) > 2048 || strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return errMetadata
		}
	}
	return nil
}

func (s *Server) validateWASM(ctx context.Context, wasm []byte, abiVersion uint8) error {
	if len(wasm) == 0 {
		return errArtifact
	}
	if s.validateArtifact != nil {
		if err := s.validateArtifact(ctx, wasm, int(abiVersion)); err != nil {
			return fmt.Errorf("%w: %v", errArtifact, err)
		}
	}
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return errArtifact
	}
	for _, imported := range compiled.ImportedFunctions() {
		module, _, ok := imported.Import()
		if !ok || (module != "wasi_snapshot_preview1" && module != "plumtree") {
			return errArtifact
		}
	}
	if _, ok := compiled.ExportedFunctions()["_start"]; !ok {
		return errArtifact
	}
	return nil
}

func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.metadataMax))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || ensureSingleJSON(dec) != nil {
		s.problemForError(w, errMalformed)
		return false
	}
	return true
}

func ensureSingleJSON(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errMalformed
	}
	return nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, errMalformed
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: part", errTooLarge)
	}
	return b, nil
}

func routeSegments(path string) ([]string, bool) {
	rest := strings.TrimPrefix(path, control.APIPrefix+"/")
	if rest == path || rest == "" {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	for i := range parts {
		if parts[i] == "" {
			return nil, false
		}
	}
	return parts, true
}

type problem struct {
	status                                int
	code, title, detail, expected, actual string
	limit                                 int64
}

func (s *Server) problemForError(w http.ResponseWriter, err error) {
	p := problem{status: http.StatusInternalServerError, code: "internal_error", title: "Internal server error", detail: "the server could not complete the request"}
	switch {
	case errors.Is(err, errUnauthorized):
		p.status, p.code, p.title, p.detail = http.StatusUnauthorized, "unauthorized", "Unauthorized", "device authentication is required"
	case errors.Is(err, errForbidden):
		p.status, p.code, p.title, p.detail = http.StatusForbidden, "forbidden", "Forbidden", "the authenticated device is not allowed to perform this operation"
	case errors.Is(err, errVersion):
		p.status, p.code, p.title, p.detail = http.StatusConflict, "product_version_mismatch", "Product version mismatch", "pt and server must use the same Plumtree product version"
	case errors.Is(err, errMalformed):
		p.status, p.code, p.title, p.detail = http.StatusBadRequest, "malformed_request", "Malformed request", "the request is not valid"
	case errors.Is(err, errMetadata):
		p.status, p.code, p.title, p.detail = http.StatusBadRequest, "metadata_invalid", "Invalid metadata", "deployment metadata is invalid"
	case errors.Is(err, errMediaType):
		p.status, p.code, p.title, p.detail = http.StatusUnsupportedMediaType, "media_type_unsupported", "Unsupported media type", "the request media type is not supported"
	case errors.Is(err, errTooLarge):
		p.status, p.code, p.title, p.detail = http.StatusRequestEntityTooLarge, "request_too_large", "Request too large", "the request exceeds a safe limit"
	case errors.Is(err, errABI):
		p.status, p.code, p.title, p.detail = http.StatusUnprocessableEntity, "abi_unsupported", "Unsupported ABI", "the artifact ABI is not supported"
	case errors.Is(err, errArtifact):
		p.status, p.code, p.title, p.detail = http.StatusUnprocessableEntity, "artifact_invalid", "Invalid artifact", "the uploaded artifact failed validation"
	case errors.Is(err, sqlite.ErrNotFound):
		p.status, p.code, p.title, p.detail = http.StatusNotFound, "not_found", "Not found", "the requested resource was not found"
	case errors.Is(err, sqlite.ErrConflict):
		p.status, p.code, p.title, p.detail = http.StatusConflict, "deploy_conflict", "Conflict", "the requested state conflicts with current server state"
	case errors.Is(err, sqlite.ErrQuota):
		p.status, p.code, p.title, p.detail = http.StatusTooManyRequests, "quota_exceeded", "Quota exceeded", "the configured resource quota was exceeded"
	case errors.Is(err, sqlite.ErrSuspended):
		p.status, p.code, p.title, p.detail = http.StatusForbidden, "forbidden", "Forbidden", "the resource is suspended"
	case errors.Is(err, sqlite.ErrInvalid):
		p.status, p.code, p.title, p.detail = http.StatusBadRequest, "metadata_invalid", "Invalid request", "one or more request fields are invalid"
	}
	s.problem(w, p)
}

func (s *Server) problem(w http.ResponseWriter, p problem) {
	if p.status == 0 {
		p.status = http.StatusInternalServerError
	}
	w.Header().Set(ProductVersionHeader, s.product)
	w.Header().Set("Content-Type", "application/problem+json")
	body := map[string]any{"type": "urn:plumtree:problem:" + p.code, "title": p.title, "status": p.status, "code": p.code, "detail": p.detail}
	if p.expected != "" {
		body["expected"] = p.expected
	}
	if p.actual != "" {
		body["actual"] = p.actual
	}
	if p.limit > 0 {
		body["limit"] = p.limit
	}
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set(ProductVersionHeader, s.product)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) methodNotAllowed(w http.ResponseWriter) {
	s.problem(w, problem{status: http.StatusMethodNotAllowed, code: "method_not_allowed", title: "Method not allowed", detail: "the HTTP method is not supported for this route"})
}

func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}
