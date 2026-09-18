package selfupdate

import (
	"context"
	"flag"
	"fmt"
	"io"

	"path/filepath"
	"strings"
)

// BinaryLabel is how the updater names the installed pair in messages.
const BinaryLabel = "plumtree binaries"

// ErrNoConfirmation is the refusal when the update lacks a confirmation.
var ErrNoConfirmation = fmt.Errorf("confirmation required: rerun with --yes or use an interactive terminal")

// Run performs `update [--check] [--yes] [--version TAG]`.
func (c Command) Run(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "report the available update without changing anything")
	yes := fs.Bool("yes", false, "confirm the replacement without a prompt")
	pinned := fs.String("version", "", "pin the update to a specific release tag")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprintln(out, usageUpdate)
			return nil
		}
		return fmt.Errorf("parse update flags: %w", err)
	}
	current := strings.TrimSpace(c.Version)
	if !IsRelease(current) {
		return fmt.Errorf("this %s was built from source without a release stamp, so update cannot manage it; rebuild the binary instead", c.member())
	}
	if c.Source == nil {
		c.Source = NewGitHubSource(DefaultRepo)
	}
	release, err := c.resolveTarget(context.Background(), *pinned)
	if err != nil {
		return err
	}
	if *pinned == "" && CompareVersions(current, release.Tag) >= 0 {
		_, _ = fmt.Fprintf(out, "%s is already on %s, the latest stable release\n", BinaryLabel, current)
		return nil
	}
	if *check {
		_, _ = fmt.Fprintf(out, "update available for %s: %s → %s\n", c.member(), current, release.Tag)
		_, _ = fmt.Fprintln(out, "rerun update without --check to apply it")
		return nil
	}
	directory, err := c.installDirectory()
	if err != nil {
		return err
	}
	c.warnIfPackageManagerOwned(out, directory)
	if err := IsWritable(directory); err != nil {
		c.printManualInstall(out, directory)
		return fmt.Errorf("install directory %s is not writable: %v; nothing was modified", directory, err)
	}
	targets, err := PairTargets(directory)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("no %s binaries found next to %q; update only manages its own install", strings.Join(PairMembers, ", "), c.member())
	}
	if !*yes && !c.confirm("Replace "+strings.Join(displayTargets(targets), ", ")+" with release "+release.Tag+"?") {
		_, _ = fmt.Fprintln(out, "nothing was modified")
		return ErrNoConfirmation
	}
	payloads, err := c.downloadVerified(context.Background(), release, targets)
	if err != nil {
		return err
	}
	if err := ReplaceAll(directory, payloads); err != nil {
		return err
	}
	CleanupDisplaced(directory)
	_, _ = fmt.Fprintf(out, "updated %s to %s; running processes keep the previous binary until their next restart\n", strings.Join(displayTargets(targets), ", "), release.Tag)
	return nil
}

const usageUpdate = "usage: pt update [--check] [--yes] [--version vX.Y.Z]"

func (c Command) confirm(prompt string) bool {
	if c.Confirm == nil {
		return false
	}
	return c.Confirm(prompt)
}

// member reports which member of the pair the command runs from.
func (c Command) member() string {
	return strings.TrimSuffix(filepath.Base(c.ExePath), ".exe")
}

// resolveTarget pins to an exact release when asked, else takes the latest
// stable release, which excludes prereleases and drafts.
func (c Command) resolveTarget(ctx context.Context, pinned string) (Release, error) {
	if pinned == "" {
		release, err := c.Source.Latest(ctx)
		if err != nil {
			return Release{}, fmt.Errorf("resolve latest stable release: %w", err)
		}
		return release, nil
	}
	release, err := c.Source.ReleaseByTag(ctx, pinned)
	if err != nil {
		return Release{}, fmt.Errorf("resolve release %s: %w", pinned, err)
	}
	return release, nil
}

// installDirectory resolves where the running executable lives.
func (c Command) installDirectory() (string, error) {
	if c.ExePath == "" {
		return "", fmt.Errorf("no executable path")
	}
	return filepath.Dir(c.ExePath), nil
}

// downloadVerified fetches the checksum manifest and each target's asset and
// verifies them before any file on disk is touched.
func (c Command) downloadVerified(ctx context.Context, release Release, targets []Target) (map[string][]byte, error) {
	manifestURL, found := release.Assets["checksums.txt"]
	if !found {
		return nil, fmt.Errorf("release %s does not ship a checksums.txt manifest", release.Tag)
	}
	manifest, err := c.Source.Fetch(ctx, manifestURL)
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}
	checksums, err := ReadChecksums(manifest)
	if err != nil {
		return nil, fmt.Errorf("release %s: %w", release.Tag, err)
	}
	payloads := make(map[string][]byte, len(targets))
	for _, target := range targets {
		asset := AssetName(target.Name)
		url, found := release.Assets[asset]
		if !found {
			return nil, fmt.Errorf("release %s does not ship a %s asset for this platform", release.Tag, asset)
		}
		payload, err := c.Source.Fetch(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", asset, err)
		}
		expected := checksums[asset]
		if expected == "" {
			return nil, fmt.Errorf("release %s checksums.txt has no entry for %s", release.Tag, asset)
		}
		if got := Sha256Hex(payload); got != expected {
			return nil, fmt.Errorf("checksum mismatch for %s: manifest expects %s, download is %s", asset, expected, got)
		}
		payloads[target.Name] = payload
	}
	return payloads, nil
}

var packageManagerMarkers = []string{
	"/Cellar/",
	"/Caskroom/",
	"/nix/store/",
	"/nix/var/nix/profiles/",
}

// warnIfPackageManagerOwned prints a warning when update would fight the
// managing package manager; the update still proceeds.
func (c Command) warnIfPackageManagerOwned(out io.Writer, directory string) {
	for _, marker := range packageManagerMarkers {
		if strings.Contains(directory, marker) {
			_, _ = fmt.Fprintf(out, "warning: %s looks package-manager-owned; update may fight the managing package manager\n", directory)
			return
		}
	}
}

// printManualInstall prints the operator's alternative: manual download and
// install commands, printed before anything on disk was modified.
func (c Command) printManualInstall(out io.Writer, directory string) {
	_, _ = fmt.Fprintln(out, "update after gaining write access, or install each binary by hand:")
	for _, member := range PairMembers {
		asset := AssetName(member)
		_, _ = fmt.Fprintf(out, "  curl -fLo %s https://github.com/%s/releases/latest/download/%s\n", asset, DefaultRepo, asset)
		_, _ = fmt.Fprintf(out, "  install -m 755 %s %s\n", asset, directory)
	}
}

func displayTargets(targets []Target) []string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, filepath.Base(target.Path))
	}
	return names
}
