// Package buildprotocol defines the bounded source-build request and result protocol
// shared by the root-owned HTTP API and the isolated legacy build worker.
package buildprotocol

import (
	"crypto/sha256"
	"encoding/hex"
)

// Stage identifies the pipeline stage that rejected or failed a build.
type Stage string

const (
	StageSource  Stage = "source"
	StagePolicy  Stage = "policy"
	StageCompile Stage = "compile"
	StageTimeout Stage = "timeout"
	StageWorker  Stage = "worker"
)

// Request is one source-build job. Source is serialized as base64 in JSON.
type Request struct {
	Source     []byte `json:"source"`
	ABIVersion uint8  `json:"abiVersion"`
}

// Result is the outcome of a source build. Exactly one of Failure or the
// artifact fields is meaningful depending on Success.
type Result struct {
	Success         bool     `json:"success"`
	WASM            []byte   `json:"wasm,omitempty"`
	Digest          string   `json:"digest,omitempty"`
	SizeBytes       int64    `json:"sizeBytes,omitempty"`
	ABIVersion      uint8    `json:"abiVersion"`
	CompilerVersion string   `json:"compilerVersion,omitempty"`
	BuildLog        string   `json:"buildLog,omitempty"`
	DurationMillis  int64    `json:"durationMillis"`
	Failure         *Failure `json:"failure,omitempty"`
}

// Failure is a structured author-facing build error.
type Failure struct {
	Stage   Stage  `json:"stage"`
	Message string `json:"message"`
	Log     string `json:"log,omitempty"`
}

func (f *Failure) Error() string { return string(f.Stage) + ": " + f.Message }

// SourceDigest returns the content-addressed digest used for uploaded source
// and generated WASM bytes.
func SourceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
