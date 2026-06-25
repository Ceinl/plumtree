package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

// createDeployArtifact materializes the WASM artifact for a deploy. When the
// request carries source and a build backend is configured, it compiles the
// source in the sandbox and derives the artifact from the build output, never
// trusting client-supplied WASM.
func (s *Server) createDeployArtifact(ctx context.Context, req devDeployRequest) (control.Artifact, *buildworker.Failure, error) {
	metadata := cloneMetadata(req.BuildMetadata)
	if req.AppType != "" {
		metadata["app_type"] = req.AppType
	}

	digest := req.ArtifactDigest
	size := req.ArtifactSizeBytes
	abiVersion := req.ABIVersion
	wasm := req.WASM

	if len(req.Source) > 0 {
		if s.build == nil {
			return control.Artifact{}, nil, errors.New("server-side build is not enabled")
		}
		res, err := s.build.Build(ctx, buildworker.Request{Source: req.Source, ABIVersion: req.ABIVersion})
		if err != nil {
			return control.Artifact{}, nil, err
		}
		if !res.Success {
			return control.Artifact{}, buildFailure(res.Failure), nil
		}
		digest = res.Digest
		size = res.SizeBytes
		abiVersion = res.ABIVersion
		wasm = res.WASM
		metadata["builder"] = "server"
		if res.CompilerVersion != "" {
			metadata["compiler"] = res.CompilerVersion
		}
		metadata["build_duration_ms"] = strconv.FormatInt(res.DurationMillis, 10)
	}

	artifact, err := s.store.CreateArtifact(control.ArtifactInput{
		Digest:        digest,
		SizeBytes:     size,
		ABIVersion:    abiVersion,
		BuildMetadata: metadata,
	})
	if err != nil {
		return control.Artifact{}, nil, err
	}
	if len(wasm) > 0 {
		if err := s.store.PutArtifactBytes(artifact.ID, wasm); err != nil {
			return control.Artifact{}, nil, err
		}
	}
	return artifact, nil, nil
}

func buildFailure(failure *buildworker.Failure) *buildworker.Failure {
	if failure != nil {
		return failure
	}
	return &buildworker.Failure{Stage: buildworker.StageWorker, Message: "build failed"}
}

func writeBuildFailure(w http.ResponseWriter, f *buildworker.Failure) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":   "build failed",
		"stage":   f.Stage,
		"message": f.Message,
		"log":     truncateLog(f.Log),
	})
}

func truncateLog(log string) string {
	const max = 16 << 10
	if len(log) <= max {
		return log
	}
	return log[len(log)-max:]
}
