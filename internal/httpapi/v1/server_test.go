package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/server/identity"
	"github.com/Ceinl/plumtree/internal/sqlite"
	"github.com/Ceinl/plumtree/sdk/abi"
)

func newTestServer(t *testing.T) (*Server, Principal, *sqlite.Repository) {
	t.Helper()
	repo, err := sqlite.OpenRepository(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.SetServerIdentity(context.Background(), sqlite.ServerIdentity{ID: "server-1", SSHHostKeyAlgorithm: "ssh-ed25519", SSHHostKeyFingerprint: "SHA256:host"}); err != nil {
		t.Fatal(err)
	}
	author, device, err := repo.RegisterAuthor(context.Background(), sqlite.RegistrationInput{AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop", PublicKey: "key", Fingerprint: "fp", RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier")})
	if err != nil || author.ID == "" || device.ID == "" {
		t.Fatalf("registration=%+v %+v err=%v", author, device, err)
	}
	cfg := serverconfig.Default()
	cfg.Roles.Control = true
	cfg.Exposure.SSH = serverconfig.ExposureGate{Enabled: true, Address: "local"}
	identities, err := identity.New(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Repository: repo, Identity: identities, ProductVersion: "devel+test", ABIVersion: abi.Version})
	if err != nil {
		t.Fatal(err)
	}
	return server, Principal{ServerID: "server-1", AuthorID: "author-1", DeviceID: "device-1", Fingerprint: "fp"}, repo
}

func TestVersionAndAuthenticationBeforeBody(t *testing.T) {
	server, principal, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	req = req.WithContext(WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get(ProductVersionHeader) != "devel+test" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("version status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body)
	}
	var version struct {
		ABI uint8 `json:"abiVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &version); err != nil || version.ABI != abi.Version {
		t.Fatalf("version=%s err=%v", rec.Body, err)
	}
	read := &countingReader{}
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", read)
	bad.Header.Set(ProductVersionHeader, "devel+test")
	bad.Header.Set("Content-Type", "multipart/form-data; boundary=unused")
	badRec := httptest.NewRecorder()
	server.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized || read.reads != 0 {
		t.Fatalf("unauthenticated status=%d body=%s bodyReads=%d", badRec.Code, badRec.Body, read.reads)
	}
}

func TestDeviceInvitationIsBoundAndSecretIsReturnedOnce(t *testing.T) {
	server, principal, repo := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(`{"deviceName":"phone"}`))
	req.Header.Set(ProductVersionHeader, "devel+test")
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var response struct {
		Invitation struct {
			ID, Secret, DeviceName string
		} `json:"invitation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Invitation.ID == "" || len(response.Invitation.Secret) < 16 || response.Invitation.DeviceName != "phone" {
		t.Fatalf("response=%s", rec.Body)
	}
	credential, err := repo.EnrollmentCredential(context.Background(), response.Invitation.ID)
	if err != nil || credential.DeviceName != "phone" || string(credential.Verifier) == response.Invitation.Secret {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}

func TestExactMultipartDeploymentDerivesArtifactAndProblems(t *testing.T) {
	server, principal, repo := newTestServer(t)
	wasm := minimalWASM()
	body, contentType := multipartBody(t, deploymentMetadata{AppName: "counter", AppType: "cli", AccessMode: "public", ABIVersion: abi.Version, SourceDigest: digestBytes([]byte("source"))}, wasm)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", body)
	req.Header.Set(ProductVersionHeader, "devel+test")
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("deploy status=%d body=%s", rec.Code, rec.Body)
	}
	var response struct {
		App      struct{ ID, ActiveDeployID string } `json:"app"`
		Deploy   struct{ ID string }                 `json:"deploy"`
		Artifact struct {
			Digest     string
			SizeBytes  int64
			ABIVersion uint8
		} `json:"artifact"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.App.ID == "" || response.Deploy.ID == "" || response.App.ActiveDeployID != response.Deploy.ID || response.Artifact.Digest != digestBytes(wasm) || response.Artifact.SizeBytes != int64(len(wasm)) || response.Artifact.ABIVersion != abi.Version {
		t.Fatalf("response=%s", rec.Body)
	}
	if _, err := repo.ResolveRunnable(context.Background(), principal.AuthorID, "counter"); err != nil {
		t.Fatalf("runnable=%v", err)
	}

	mismatch, mismatchContentType := multipartBody(t, deploymentMetadata{AppName: "bad", AppType: "cli", AccessMode: "public", ABIVersion: abi.Version, SourceDigest: digestBytes([]byte("source"))}, wasm)
	wrong := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", mismatch)
	wrong.Header.Set(ProductVersionHeader, "other")
	wrong.Header.Set("Content-Type", mismatchContentType)
	wrong = wrong.WithContext(WithPrincipal(wrong.Context(), principal))
	wrongRec := httptest.NewRecorder()
	server.ServeHTTP(wrongRec, wrong)
	if wrongRec.Code != http.StatusConflict || wrongRec.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(wrongRec.Body.String(), "product_version_mismatch") {
		t.Fatalf("mismatch status=%d headers=%v body=%s", wrongRec.Code, wrongRec.Header(), wrongRec.Body)
	}
}

func TestSecretAndEgressRoutesDoNotExposeSecretValues(t *testing.T) {
	server, principal, repo := newTestServer(t)
	if _, err := repo.CreateApp(context.Background(), sqlite.AppInput{ID: "app-1", AuthorID: principal.AuthorID, Name: "counter", Kind: "cli", AccessMode: "public", CreatedByDeviceID: principal.DeviceID}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(ProductVersionHeader, "devel+test")
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}
	secret := request(http.MethodPut, "/api/v1/apps/app-1/secrets/API_KEY", `{"value":"do-not-return"}`)
	if secret.Code != http.StatusOK || strings.Contains(secret.Body.String(), "do-not-return") {
		t.Fatalf("secret write status=%d body=%s", secret.Code, secret.Body)
	}
	listed := request(http.MethodGet, "/api/v1/apps/app-1/secrets", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "do-not-return") || !strings.Contains(listed.Body.String(), "API_KEY") {
		t.Fatalf("secret list status=%d body=%s", listed.Code, listed.Body)
	}
	egress := request(http.MethodPost, "/api/v1/apps/app-1/egress", `{"host":"api.example.com"}`)
	if egress.Code != http.StatusOK || !strings.Contains(egress.Body.String(), "api.example.com") {
		t.Fatalf("egress write status=%d body=%s", egress.Code, egress.Body)
	}
	listedEgress := request(http.MethodGet, "/api/v1/apps/app-1/egress", "")
	if listedEgress.Code != http.StatusOK || !strings.Contains(listedEgress.Body.String(), "api.example.com") {
		t.Fatalf("egress list status=%d body=%s", listedEgress.Code, listedEgress.Body)
	}
}

type countingReader struct{ reads int }

func (r *countingReader) Read([]byte) (int, error) { r.reads++; return 0, context.Canceled }

func multipartBody(t *testing.T, metadata deploymentMetadata, wasm []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader["Content-Disposition"] = []string{`form-data; name="metadata"`}
	metadataHeader["Content-Type"] = []string{"application/json"}
	part, err := writer.CreatePart(metadataHeader)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(part).Encode(metadata); err != nil {
		t.Fatal(err)
	}
	artifactHeader := make(textproto.MIMEHeader)
	artifactHeader["Content-Disposition"] = []string{`form-data; name="artifact"; filename="app.wasm"`}
	artifactHeader["Content-Type"] = []string{"application/wasm"}
	part, err = writer.CreatePart(artifactHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(wasm); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func minimalWASM() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0a, 0x01, 0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b}
}

func TestNewIDPrefixesUniqueIdentifiers(t *testing.T) {
	first, err := newID("app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "app_") || len(first) != len("app_")+32 {
		t.Fatalf("newID = %q", first)
	}
	second, err := newID("app")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("newID repeated %q", first)
	}
}

func TestInternalErrorsProduceTheInternalErrorProblem(t *testing.T) {
	server, _, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	server.problemForError(rec, errors.New("generate app id: entropy exhausted"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Type  string `json:"type"`
		Code  string `json:"code"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "internal_error" || body.Type != "urn:plumtree:problem:internal_error" || body.Title == "" {
		t.Fatalf("problem body = %+v", body)
	}
}
