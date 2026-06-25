package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

type devDeployRequest struct {
	AppName           string             `json:"appName"`
	AppType           string             `json:"appType"`
	Visibility        control.Visibility `json:"visibility"`
	ArtifactDigest    string             `json:"artifactDigest"`
	ArtifactSizeBytes int64              `json:"artifactSizeBytes"`
	ABIVersion        uint8              `json:"abiVersion"`
	SourceDigest      string             `json:"sourceDigest"`
	BuildMetadata     map[string]string  `json:"buildMetadata"`
	WASM              []byte             `json:"wasm,omitempty"`
	// Source is a packed app source archive (see buildworker.PackSource). When
	// present and a build backend is configured, the control plane compiles it
	// server-side and ignores any client-supplied WASM/digest.
	Source []byte `json:"source,omitempty"`
}

func (s *Server) handleDevDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeDevDeploy(w, r) {
		return
	}

	req, err := readDevDeployRequest(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	artifact, failure, err := s.createDeployArtifact(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if failure != nil {
		writeBuildFailure(w, failure)
		return
	}

	claimToken, err := newClaimToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	deploy, err := s.store.CreateDeployClaim(control.DeployClaimInput{
		AppName:        req.AppName,
		AppType:        req.AppType,
		Visibility:     req.Visibility,
		ArtifactID:     artifact.ID,
		SourceDigest:   req.SourceDigest,
		ClaimTokenHash: hashClaimToken(claimToken),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.scheduleDeployClaimCleanup()

	claimURL := s.claimURL(r, deploy.ID, claimToken)
	writeJSON(w, http.StatusCreated, devDeployResponse("", control.App{
		Name:       deploy.AppName,
		Visibility: deploy.Visibility,
	}, deploy, false, claimURL))
}

func (s *Server) handleDevDeployPath(w http.ResponseWriter, r *http.Request) {
	// Route subresources: /api/dev/deploy/{deployID}/{secrets|egress}[/{rest}].
	rest := strings.TrimPrefix(r.URL.Path, "/api/dev/deploy/")
	if deployID, sub, tail, ok := splitSubresourcePath(rest); ok {
		switch sub {
		case "secrets":
			s.handleDevSecrets(w, r, deployID, tail)
			return
		case "egress":
			s.handleDevEgress(w, r, deployID, tail)
			return
		}
	}
	if r.Method == http.MethodPut {
		s.handleDevDeployUpdate(w, r)
		return
	}
	if r.Method == http.MethodGet {
		s.handleDevDeployInspect(w, r)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// splitSubresourcePath parses "{deployID}/{sub}" or "{deployID}/{sub}/{tail}"
// out of the path remainder after the /api/dev/deploy/ prefix.
func splitSubresourcePath(rest string) (deployID, sub, tail string, ok bool) {
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	if len(parts) == 3 {
		tail = parts[2]
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", "", false
	}
	return id, parts[1], tail, true
}

func (s *Server) handleDevDeployUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeDevDeploy(w, r) {
		return
	}
	deployID, ok := pathTail("/api/dev/deploy/", r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	claimToken := bearerToken(r.Header.Get("Authorization"))
	if claimToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing deploy claim token"})
		return
	}

	req, err := readDevDeployRequest(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	artifact, failure, err := s.createDeployArtifact(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if failure != nil {
		writeBuildFailure(w, failure)
		return
	}

	app, deploy, claimed, err := s.store.UpdateDeployClaim(deployID, control.DeployClaimInput{
		AppName:        req.AppName,
		AppType:        req.AppType,
		Visibility:     req.Visibility,
		ArtifactID:     artifact.ID,
		SourceDigest:   req.SourceDigest,
		ClaimTokenHash: hashClaimToken(claimToken),
	})
	if err != nil {
		writeDeployClaimError(w, err)
		return
	}
	if !claimed {
		s.scheduleDeployClaimCleanup()
	}

	ownerHandle := ""
	if claimed {
		owner, err := s.store.GetOwner(app.OwnerID)
		if err != nil {
			writeControlError(w, err)
			return
		}
		ownerHandle = owner.Handle
	}
	writeJSON(w, http.StatusOK, devDeployResponse(ownerHandle, app, deploy, claimed, ""))
}

func (s *Server) handleDevDeployInspect(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeDevDeploy(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/dev/deploy/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	deployID, err := url.PathUnescape(parts[0])
	if err != nil || deployID == "" {
		http.NotFound(w, r)
		return
	}
	claimToken := bearerToken(r.Header.Get("Authorization"))
	if claimToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing deploy claim token"})
		return
	}
	if _, err := s.store.DeleteExpiredDeployClaims(); err != nil {
		writeControlError(w, err)
		return
	}
	deploy, app, owner, artifact, err := s.store.InspectDeployClaim(deployID, hashClaimToken(claimToken))
	if err != nil {
		writeDeployClaimError(w, err)
		return
	}
	if len(parts) == 2 {
		if parts[1] != "logs" {
			http.NotFound(w, r)
			return
		}
		s.writeDeployLogs(w, app)
		return
	}
	writeJSON(w, http.StatusOK, inspectResponse(owner, app, deploy, artifact))
}

func readDevDeployRequest(w http.ResponseWriter, r *http.Request) (devDeployRequest, error) {
	var req devDeployRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		return req, err
	}
	if req.Visibility == "" {
		req.Visibility = control.VisibilityPublic
	}
	if len(req.Source) > 0 {
		// The control plane owns the source digest when it receives source.
		req.SourceDigest = buildworker.SourceDigest(req.Source)
	}
	return req, nil
}
