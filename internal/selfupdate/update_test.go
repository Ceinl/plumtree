package selfupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateReplacesExistingBinariesWithPermissionsPreserved(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	seedBinary(t, directory, "plumtree", "old-server", 0o700)
	source, payload := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	var out, errOut strings.Builder
	if err := command.Run([]string{"--yes"}, &out, &errOut); err != nil {
		t.Fatalf("update: %v (out=%q err=%q)", err, out.String(), errOut.String())
	}
	for name, want := range map[string][]byte{"pt": payload, "plumtree": payload} {
		path := filepath.Join(directory, name)
		if got := readBytes(t, path); string(got) != string(want) {
			t.Errorf("%s content = %q, want release payload", name, got)
		}
	}
	if mode := readMode(t, filepath.Join(directory, "pt")); mode != 0o755 {
		t.Errorf("pt permissions = %v, want preserved 0755", mode)
	}
	if mode := readMode(t, filepath.Join(directory, "plumtree")); mode != 0o700 {
		t.Errorf("plumtree permissions = %v, want preserved 0700", mode)
	}
	for _, member := range PairMembers {
		if _, err := os.Stat(filepath.Join(directory, member+".ptbak")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("displaced binary %s.ptbak was not cleaned up: %v", member, err)
		}
	}
	if !strings.Contains(out.String(), fakeStableTag) {
		t.Errorf("update output %q does not name the release", out.String())
	}
}

func TestUpdateOnlyManagesMembersFoundNextToIt(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	source, payload := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	var out, errOut strings.Builder
	if err := command.Run([]string{"--yes"}, &out, &errOut); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != string(payload) {
		t.Errorf("pt content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(directory, "plumtree")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("update created a plumtree binary that was not installed: %v", err)
	}
}

func TestUpdateRefusesNonReleaseBuild(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "dev-pt", 0o755)
	for _, stamp := range []string{"", "dev"} {
		source, _ := stagedRelease(fakeStableTag, "")
		command := newCommand(directory, stamp, source)
		var out, errOut strings.Builder
		err := command.Run([]string{"--yes"}, &out, &errOut)
		if err == nil {
			t.Fatalf("update with stamp %q: expected refusal", stamp)
		}
		if !strings.Contains(err.Error(), "release stamp") || !strings.Contains(err.Error(), "rebuild") {
			t.Errorf("refusal error %q lacks dev-build guidance", err)
		}
		if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != "dev-pt" {
			t.Errorf("pt content = %q after refused update", got)
		}
	}
}

func TestUpdateCheckReportsPlanWithoutModifyingAnything(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	seedBinary(t, directory, "plumtree", "old-server", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	var out, errOut strings.Builder
	if err := command.Run([]string{"--check"}, &out, &errOut); err != nil {
		t.Fatalf("update --check: %v", err)
	}
	if !strings.Contains(out.String(), "v1.2.3 → "+fakeStableTag) {
		t.Errorf("check output %q does not lay out the plan", out.String())
	}
	for name, want := range map[string]string{"pt": "old-pt", "plumtree": "old-server"} {
		if got := readBytes(t, filepath.Join(directory, name)); string(got) != want {
			t.Errorf("%s content = %q after --check", name, got)
		}
	}
}

func TestUpdateAlreadyCurrentChangesNothing(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "recent-pt", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, fakeStableTag, source)
	var out, errOut strings.Builder
	if err := command.Run(nil, &out, &errOut); err != nil {
		t.Fatalf("update when current: %v", err)
	}
	if !strings.Contains(out.String(), "already on") {
		t.Errorf("output %q does not report the current version", out.String())
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != "recent-pt" {
		t.Errorf("pt content = %q", got)
	}
}

func TestNewerRunningVersionReportsAlreadyCurrentWhilePinnedReinstallNeedsConfirmation(t *testing.T) {
	if CompareVersions("v1.2.9", "v1.2.10") >= 0 {
		t.Fatal("numeric version comparison failed")
	}
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "newer-pt", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v9.9.9", source)
	var out, errOut strings.Builder
	if err := command.Run([]string{"--yes"}, &out, &errOut); err != nil {
		t.Fatalf("update with a newer running version: %v", err)
	}
	if !strings.Contains(out.String(), "already on") {
		t.Errorf("output %q does not report the current version", out.String())
	}
	// Pinning an older release is the supported way to return to it, and it
	// requires confirmation like any other replacement.
	source, _ = stagedRelease("v1.2.0", "")
	command = newCommand(directory, fakeStableTag, source)
	out, errOut = strings.Builder{}, strings.Builder{}
	err := command.Run([]string{"--version", "v1.2.0"}, &out, &errOut)
	if !errors.Is(err, ErrNoConfirmation) {
		t.Fatalf("pinned update without confirmation: err = %v (out=%q err=%q)", err, out.String(), errOut.String())
	}
}

func TestUpdateRefusesWithoutConfirmationAndChangesNothing(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	seedBinary(t, directory, "plumtree", "old-server", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source) // Confirm nil on purpose
	var out, errOut strings.Builder
	err := command.Run(nil, &out, &errOut)
	if !errors.Is(err, ErrNoConfirmation) {
		t.Fatalf("update without --yes: err = %v", err)
	}
	if !strings.Contains(out.String(), "nothing was modified") {
		t.Errorf("output %q does not say nothing was modified", out.String())
	}
	for name, want := range map[string]string{"pt": "old-pt", "plumtree": "old-server"} {
		if got := readBytes(t, filepath.Join(directory, name)); string(got) != want {
			t.Errorf("%s content = %q after refusal", name, got)
		}
	}
}

func TestUpdateWithYesFlagSkipsThePrompt(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	source, payload := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	var out, errOut strings.Builder
	if err := command.Run([]string{"--yes"}, &out, &errOut); err != nil {
		t.Fatalf("update --yes: %v", err)
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != string(payload) {
		t.Errorf("pt content = %q", got)
	}
}

func TestUpdateWithConfirmationPromptRuns(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	source, payload := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	command.Confirm = func(prompt string) bool {
		if !strings.Contains(prompt, fakeStableTag) {
			t.Errorf("prompt %q does not name the release", prompt)
		}
		return true
	}
	var out, errOut strings.Builder
	if err := command.Run(nil, &out, &errOut); err != nil {
		t.Fatalf("update with prompt: %v", err)
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != string(payload) {
		t.Errorf("pt content = %q", got)
	}
}

func TestChecksumMismatchLeavesBinariesUntouched(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	seedBinary(t, directory, "plumtree", "old-server", 0o755)
	source, _ := stagedRelease(fakeStableTag, strings.Repeat("9", 64)+"  pt-linux-amd64\n")
	command := newCommand(directory, "v1.2.3", source)
	var out, errOut strings.Builder
	err := command.Run([]string{"--yes"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("update with bad checksum: err = %v", err)
	}
	for name, want := range map[string]string{"pt": "old-pt", "plumtree": "old-server"} {
		if got := readBytes(t, filepath.Join(directory, name)); string(got) != want {
			t.Errorf("%s content = %q after failed verification", name, got)
		}
	}
}

func TestUpdateRollsBackWhenOneMemberReplacementFails(t *testing.T) {
	directory := t.TempDir()
	ptPath := seedBinary(t, directory, "pt", "old-pt", 0o755)
	serverPath := seedBinary(t, directory, "plumtree", "old-server", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	command.Fault = func(target Target) error {
		if target.Name == "plumtree" {
			return errors.New("forced failure")
		}
		return nil
	}
	var out, errOut strings.Builder
	err := command.Run([]string{"--yes"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "forced failure") {
		t.Fatalf("mid-swap failure: err = %v", err)
	}
	if got := readBytes(t, ptPath); string(got) != "old-pt" {
		t.Errorf("pt content = %q after rollback", got)
	}
	if got := readBytes(t, serverPath); string(got) != "old-server" {
		t.Errorf("plumtree content = %q after rollback", got)
	}
	if _, err := os.Stat(filepath.Join(directory, "plumtree.ptbak")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("displaced plumtree survived rollback: %v", err)
	}
}

func TestUnwritableDirectoryPrintsManualInstructionsAndChangesNothing(t *testing.T) {
	directory := t.TempDir()
	ptPath := seedBinary(t, directory, "pt", "old-pt", 0o755)
	unwritable := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(unwritable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unwritable, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o755) })
	source, _ := stagedRelease(fakeStableTag, "")
	command := Command{ExePath: filepath.Join(unwritable, "pt"), Version: "v1.2.3", Source: source}
	var out, errOut strings.Builder
	err := command.Run([]string{"--yes"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("unwritable update: err = %v", err)
	}
	outText := out.String() + errOut.String()
	if !strings.Contains(outText, "install each binary by hand") || !strings.Contains(outText, "latest/download/") {
		t.Errorf("output %q lacks manual instructions", outText)
	}
	if got := readBytes(t, ptPath); string(got) != "old-pt" {
		t.Errorf("pt content = %q", got)
	}
}

func TestPackageManagerOwnedLocationWarns(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "Cellar", "plumtree", "1.2.3", "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	source, payload := stagedRelease(fakeStableTag, "")
	command := newCommand(directory, "v1.2.3", source)
	var out, errOut strings.Builder
	if err := command.Run([]string{"--yes"}, &out, &errOut); err != nil {
		t.Fatalf("update inside a package-manager-looking directory: %v", err)
	}
	if !strings.Contains(out.String(), "package-manager-owned") {
		t.Errorf("output %q lacks the package-manager warning", out.String())
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != string(payload) {
		t.Errorf("pt content = %q", got)
	}
}

func TestReleaseLayoutMissingChecksumManifestFailsLoudly(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	broken := source
	delete(broken.payloads, "https://fake/releases/download/"+fakeStableTag+"/checksums.txt")
	command := newCommand(directory, "v1.2.3", broken)
	var out, errOut strings.Builder
	err := command.Run([]string{"--yes"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("missing manifest: err = %v", err)
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != "old-pt" {
		t.Errorf("pt content = %q", got)
	}
}

func TestReleaseLayoutMissingPlatformAssetFailsLoudly(t *testing.T) {
	directory := t.TempDir()
	seedBinary(t, directory, "pt", "old-pt", 0o755)
	source, _ := stagedRelease(fakeStableTag, "")
	broken := source
	delete(broken.payloads, "https://fake/releases/download/"+fakeStableTag+"/"+AssetName("pt"))
	command := newCommand(directory, "v1.2.3", broken)
	var out, errOut strings.Builder
	err := command.Run([]string{"--yes"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), AssetName("pt")) {
		t.Fatalf("missing platform asset: err = %v (out=%q err=%q)", err, out.String(), errOut.String())
	}
	if got := readBytes(t, filepath.Join(directory, "pt")); string(got) != "old-pt" {
		t.Errorf("pt content = %q", got)
	}
}

func TestCompareVersionsMatrix(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "v1.2.10", -1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"dev", "v0.0.1", -1},
		{"v1.2.3", "v1.2.3-rc1", 1},
	}
	for _, caseAt := range cases {
		if got := CompareVersions(caseAt.a, caseAt.b); got != caseAt.want {
			t.Errorf("CompareVersions(%s, %s) = %d, want %d", caseAt.a, caseAt.b, got, caseAt.want)
		}
	}
}

func TestNotice(t *testing.T) {
	stable := noticeRecord{Checked: "2026-09-18T00:00:00Z", Stable: "v9.0.0"}
	path := filepath.Join(t.TempDir(), "cache", "check.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustJSON(t, stable), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CachePathEnv, path)
	noCacheDir := func() (string, error) { return "", errors.New("no cache dir") }
	now := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	if hint := PassiveHint(noCacheDir, "v1.0.0", now); !strings.Contains(hint, "v9.0.0") {
		t.Fatalf("hint = %q, want the newer release name", hint)
	}
	// Opt out stays quiet.
	t.Setenv(OptOutEnv, "1")
	if got := PassiveHint(noCacheDir, "v1.0.0", now); got != "" {
		t.Errorf("opted-out hint = %q", got)
	}
	os.Unsetenv(OptOutEnv)
	// Dev builds never hint.
	if got := PassiveHint(noCacheDir, "", now); got != "" {
		t.Errorf("dev-build hint = %q", got)
	}
	// A stale cache stays silent.
	if err := os.WriteFile(path, mustJSON(t, noticeRecord{Checked: "2025-01-01T00:00:00Z", Stable: "v9.0.0"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PassiveHint(noCacheDir, "v1.0.0", now); got != "" {
		t.Errorf("stale cache produced hint %q", got)
	}
	// Cached knowledge of an older release stays silent too.
	if err := os.WriteFile(path, mustJSON(t, noticeRecord{Checked: "2026-09-18T00:00:00Z", Stable: "v0.9.0"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PassiveHint(noCacheDir, "v1.0.0", now); got != "" {
		t.Errorf("older cached release produced hint %q", got)
	}
}

func mustJSON(t *testing.T, record noticeRecord) []byte {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// RunCommand is the shared dispatch surface of both binaries: `version`
// prints the build identity, a refused update maps to an error (exit 1 for
// every caller), and other subcommands are not its business.
func TestRunCommandDispatch(t *testing.T) {
	var out bytes.Buffer
	if err := RunCommand(Command{Version: "v1.2.3"}, []string{"version"}, &out, io.Discard); err != nil {
		t.Fatalf("version dispatch: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "v1.2.3" {
		t.Fatalf("version output %q, want v1.2.3", got)
	}
	if err := RunCommand(Command{}, []string{"pair"}, io.Discard, io.Discard); err == nil {
		t.Fatal("non-update subcommand dispatched selfupdate RunCommand")
	}
	// A zero-stamp binary (checkout build) refuses to update, and the refusal
	// is an ordinary error callers exit non-zero on.
	directory := t.TempDir()
	command := Command{ExePath: filepath.Join(directory, "pt")}
	err := RunCommand(command, []string{"update", "--yes"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "release stamp") {
		t.Fatalf("dev-build update: err = %v, want refusal guidance", err)
	}
}
