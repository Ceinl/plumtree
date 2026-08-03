// Package workflow contains the final, clean-break pt project and
// administration workflows. It is additive until the coordinated cutover.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ceinl/plumtree/internal/build"
	"github.com/Ceinl/plumtree/internal/cli/scaffold"
	"github.com/Ceinl/plumtree/internal/runner"
)

var (
	ErrManifest = errors.New("workflow: invalid plumtree.json")
	ErrProject  = errors.New("workflow: invalid project")
	ErrConfirm  = errors.New("workflow: confirmation required")
	ErrTarget   = errors.New("workflow: target is not selected")
)

type Manifest struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Access string `json:"access"`
}

func (m Manifest) Validate() error {
	if err := scaffold.ValidateName(m.Name); err != nil {
		return fmt.Errorf("%w: %v", ErrManifest, err)
	}
	if m.Type != string(scaffold.TUI) && m.Type != string(scaffold.CLI) {
		return fmt.Errorf("%w: type must be tui or cli", ErrManifest)
	}
	if m.Access != "public" && m.Access != "restricted" {
		return fmt.Errorf("%w: access must be public or restricted", ErrManifest)
	}
	return nil
}

func ReadManifest(project string) (Manifest, error) {
	if strings.TrimSpace(project) == "" {
		return Manifest{}, fmt.Errorf("%w: project path is required", ErrProject)
	}
	file, err := os.Open(filepath.Join(project, "plumtree.json"))
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	var manifest Manifest
	dec := json.NewDecoder(io.LimitReader(file, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Manifest{}, fmt.Errorf("%w: trailing JSON", ErrManifest)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// NewScaffold requires the app mode explicitly and writes the clean manifest
// with an explicit access policy. There is no implicit target or history file.
func NewScaffold(parent, name string, kind scaffold.Kind, access string) (string, error) {
	if kind != scaffold.TUI && kind != scaffold.CLI {
		return "", fmt.Errorf("%w: choose tui or cli explicitly", ErrManifest)
	}
	if access != "public" && access != "restricted" {
		return "", fmt.Errorf("%w: choose public or restricted access explicitly", ErrManifest)
	}
	project, err := scaffold.NewWithAccess(parent, name, kind, access)
	if err != nil {
		return "", err
	}
	if _, err := ReadManifest(project); err != nil {
		return "", err
	}
	return project, nil
}

type BuildResult struct {
	Manifest Manifest
	Artifact build.Artifact
}

func Build(ctx context.Context, project, workspaceRoot string) (BuildResult, error) {
	manifest, err := ReadManifest(project)
	if err != nil {
		return BuildResult{}, err
	}
	artifact, err := (build.LocalBuilder{WorkspaceRoot: workspaceRoot}).Build(ctx, build.Project{Root: project})
	if err != nil {
		return BuildResult{}, fmt.Errorf("%w: %v", ErrProject, err)
	}
	if len(artifact.WASM) == 0 {
		return BuildResult{}, fmt.Errorf("%w: local build produced no artifact", ErrProject)
	}
	return BuildResult{Manifest: manifest, Artifact: artifact}, nil
}

// Profile is the hosted-parity local development profile. It is persistent by
// default and reset only when explicitly requested.
type Profile struct {
	Root     string
	KVPath   string
	MaxKeys  int
	MaxBytes int
	Reset    bool
}

func OpenProfile(profile Profile) (runner.Capabilities, func(), error) {
	if profile.Root == "" {
		return runner.Capabilities{}, nil, fmt.Errorf("%w: profile root is required", ErrProject)
	}
	if profile.KVPath == "" {
		profile.KVPath = filepath.Join(profile.Root, ".plumtree", "dev", "kv.json")
	}
	if profile.Reset {
		if err := os.Remove(profile.KVPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return runner.Capabilities{}, nil, fmt.Errorf("reset dev profile: %w", err)
		}
	}
	maxKeys, maxBytes := profile.MaxKeys, profile.MaxBytes
	if maxKeys == 0 {
		maxKeys = runner.DefaultMaxKeys
	}
	if maxBytes == 0 {
		maxBytes = runner.DefaultMaxBytes
	}
	kv, err := runner.NewFileStore(profile.KVPath, maxKeys, maxBytes)
	if err != nil {
		return runner.Capabilities{}, nil, fmt.Errorf("open dev profile: %w", err)
	}
	goodbye := new(string)
	caps := runner.Capabilities{KV: kv, Bus: runner.NewMemBus(),
		Auth: runner.StaticAuth{Identity: runner.Identity{User: "local", Authenticated: true, Kind: runner.IdentitySSHKey, OwnsApp: true}}, Goodbye: goodbye}
	return caps, func() {}, nil
}
