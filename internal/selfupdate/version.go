// Package selfupdate lets the pt/plumtree binaries report their release and
// replace themselves in place from the single prod-gated release channel.
package selfupdate

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

// StampedVersion is the release tag injected by the release build's linker
// flags. Builds from a git checkout without a stamp keep the zero value,
// which this package reports as "dev".
var StampedVersion string

// IsRelease reports whether the running binary carries a release stamp.
func IsRelease(stamp string) bool {
	stamp = strings.TrimSpace(stamp)
	return stamp != "" && stamp != "dev" && stamp != "(devel)"
}

// DescribeBuild renders the human-facing build identity: the stamp for
// release binaries, "dev" plus the VCS revision when available otherwise.
func DescribeBuild(stamp string) string {
	if IsRelease(stamp) {
		return strings.TrimSpace(stamp)
	}
	description := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				description += " (revision " + setting.Value + ")"
			}
		}
	}
	return description
}

// ParseVersion splits a "vX.Y.Z" version into numeric parts plus any
// prerelease suffix. It returns ok=false for non-version values such as
// "dev" or "(devel)".
func ParseVersion(value string) (parts [3]int, suffix string, ok bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	segments := strings.SplitN(value, ".", 3)
	if len(segments) < 3 {
		return parts, "", false
	}
	var suffixEach [3]string
	for i, segment := range segments {
		numeric, sub := segment, ""
		if index := strings.IndexAny(segment, "-+"); index >= 0 {
			numeric, sub = segment[:index], segment[index:]
		}
		number, err := strconv.Atoi(numeric)
		if err != nil || number < 0 {
			return parts, "", false
		}
		parts[i] = number
		suffixEach[i] = sub
	}
	return parts, suffixEach[2], true
}

// CompareVersions returns -1 when a is older than b, 0 when equal, and 1 when
// newer. A final release outranks its prereleases (-suffix); malformed
// versions only ever compare equal or lexically.
func CompareVersions(a, b string) int {
	aParts, aSuffix, aOk := ParseVersion(a)
	bParts, bSuffix, bOk := ParseVersion(b)
	if aOk && bOk {
		if aParts != bParts {
			if aParts[0] != bParts[0] {
				return sign(aParts[0] - bParts[0])
			}
			if aParts[1] != bParts[1] {
				return sign(aParts[1] - bParts[1])
			}
			return sign(aParts[2] - bParts[2])
		}
		switch {
		case aSuffix == bSuffix:
			return 0
		case bSuffix == "":
			return -1
		case aSuffix == "":
			return 1
		default:
			return strings.Compare(aSuffix, bSuffix)
		}
	}
	return strings.Compare(a, b)
}

func sign(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

// AssetName is the canonical release asset name for the running platform and
// binary, following the release asset contract.
func AssetName(binary string) string {
	name := fmt.Sprintf("%s-%s-%s", binary, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
