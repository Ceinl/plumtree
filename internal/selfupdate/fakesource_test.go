package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeStableTag = "v1.2.4"

// fakeSource backs the updater with a fake release host: release metadata,
// asset bytes, and the checksum manifest served from memory.
type fakeSource struct {
	releases map[string]Release
	payloads map[string][]byte
}

func (s fakeSource) Latest(ctx context.Context) (Release, error) {
	release, found := s.releases["latest"]
	if !found {
		return Release{}, errors.New("no latest release")
	}
	return release, nil
}

func (s fakeSource) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	release, found := s.releases[tag]
	if !found {
		return Release{}, fmt.Errorf("release %s not found", tag)
	}
	return release, nil
}

func (s fakeSource) Fetch(ctx context.Context, url string) ([]byte, error) {
	payload, found := s.payloads[url]
	if !found {
		return nil, fmt.Errorf("fake host has no %q", url)
	}
	return payload, nil
}

// stagedRelease assembles one release whose assets and checksum manifest only
// agree when overrideManifest is empty; otherwise the given manifest is
// served instead.
func stagedRelease(tag string, overrideManifest string) (fakeSource, []byte) {
	payload := []byte("new-" + tag + "-payload")
	manifestBuilder := strings.Builder{}
	for _, member := range PairMembers {
		fmt.Fprintf(&manifestBuilder, "%s  %s\n", Sha256Hex(payload), AssetName(member))
	}
	manifest := overrideManifest
	if manifest == "" {
		manifest = manifestBuilder.String()
	}
	base := "https://fake/releases/download/" + tag
	assets := map[string]string{
		"checksums.txt": base + "/checksums.txt",
	}
	payloads := map[string][]byte{
		base + "/checksums.txt": []byte(manifest),
	}
	for _, member := range PairMembers {
		assets[AssetName(member)] = base + "/" + AssetName(member)
		payloads[base+"/"+AssetName(member)] = payload
	}
	release := Release{Tag: tag, Assets: assets}
	source := fakeSource{
		releases: map[string]Release{
			"latest": release,
			tag:      release,
			"v1.2.0": {Tag: "v1.2.0", Assets: assets},
		},
		payloads: payloads,
	}
	return source, payload
}

func newCommand(directory string, stamp string, source Source) Command {
	return Command{ExePath: filepath.Join(directory, "pt"), Version: stamp, Source: source}
}

func seedBinary(t *testing.T, directory, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func readMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
