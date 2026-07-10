package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

var errBuildQueueFull = errors.New("build queue is full")

// createDeployArtifact materializes the WASM artifact for a deploy. When the
// request carries source and a build backend is configured, it compiles the
// source in the sandbox and derives the artifact from the build output, never
// trusting client-supplied WASM.
func (s *Server) buildDeployArtifact(ctx context.Context, req devDeployRequest) (control.ArtifactInput, []byte, *buildworker.Failure, error) {
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
			return control.ArtifactInput{}, nil, nil, errors.New("server-side build is not enabled")
		}
		if err := s.acquireBuildSlot(ctx); err != nil {
			return control.ArtifactInput{}, nil, nil, err
		}
		res, err := s.build.Build(ctx, buildworker.Request{Source: req.Source, ABIVersion: req.ABIVersion})
		s.releaseBuildSlot()
		if err != nil {
			return control.ArtifactInput{}, nil, nil, err
		}
		if !res.Success {
			return control.ArtifactInput{}, nil, buildFailure(res.Failure), nil
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

	in := control.ArtifactInput{
		Digest:        digest,
		SizeBytes:     size,
		ABIVersion:    abiVersion,
		BuildMetadata: metadata,
	}
	return in, wasm, nil, nil
}

func (s *Server) acquireBuildSlot(ctx context.Context) error {
	if s.buildSlots == nil {
		return nil
	}
	select {
	case s.buildSlots <- struct{}{}:
		return nil
	default:
	}
	if s.buildQueue == nil {
		return errBuildQueueFull
	}
	select {
	case s.buildQueue <- struct{}{}:
	default:
		return errBuildQueueFull
	}
	defer func() { <-s.buildQueue }()
	select {
	case s.buildSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeBuildAdmissionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errBuildQueueFull) {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) releaseBuildSlot() {
	if s.buildSlots != nil {
		<-s.buildSlots
	}
}

func (s *Server) validateDevDeployRequest(req devDeployRequest) error {
	if err := control.ValidateName(req.AppName); err != nil {
		return err
	}
	if req.AppType != "tui" && req.AppType != "cli" {
		return fmt.Errorf("%w: app type must be tui or cli", control.ErrInvalid)
	}
	if err := control.ValidateDigest("source digest", req.SourceDigest); err != nil {
		return err
	}
	if len(req.Source) > 0 {
		if s.build == nil {
			return fmt.Errorf("%w: server-side build is not enabled", control.ErrInvalid)
		}
		return nil
	}
	if err := control.ValidateDigest("artifact digest", req.ArtifactDigest); err != nil {
		return err
	}
	if req.ArtifactSizeBytes < 0 {
		return fmt.Errorf("%w: artifact size cannot be negative", control.ErrInvalid)
	}
	if len(req.WASM) > 0 {
		if int64(len(req.WASM)) != req.ArtifactSizeBytes || buildworker.SourceDigest(req.WASM) != req.ArtifactDigest {
			return fmt.Errorf("%w: wasm bytes do not match artifact size/digest", control.ErrInvalid)
		}
	}
	return nil
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
