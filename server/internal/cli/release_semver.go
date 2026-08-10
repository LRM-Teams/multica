package cli

import (
	"regexp"
	"strconv"
	"strings"
)

var gitDescribeReleasePattern = regexp.MustCompile(`-[0-9]+-g[0-9a-fA-F]{7,}(-dirty)?$`)

type parsedReleaseVersion struct {
	core [3]uint64
	pre  []string
}

// IsReleaseVersion accepts only versions that the release workflow can
// publish: vX.Y.Z or vX.Y.Z-(alpha|beta|rc).N. Generic SemVer prereleases,
// build metadata, and git-describe source builds are deliberately rejected so
// a locally named binary can never masquerade as a channel release.
func IsReleaseVersion(raw string) bool {
	_, ok := parseReleaseVersion(raw)
	return ok
}

// IsStableReleaseVersion reports whether raw is valid SemVer without a
// prerelease suffix. Stable projections must not accidentally advertise an
// alpha manifest merely because prereleases are now valid install targets.
func IsStableReleaseVersion(raw string) bool {
	version, ok := parseReleaseVersion(raw)
	return ok && len(version.pre) == 0
}

func IsPrereleaseVersion(raw string) bool {
	version, ok := parseReleaseVersion(raw)
	return ok && len(version.pre) > 0
}

// IsNewerVersion compares two valid release versions using SemVer precedence.
// Invalid values (including git-describe builds) fail closed.
func IsNewerVersion(latest, current string) bool {
	l, ok := parseReleaseVersion(latest)
	if !ok {
		return false
	}
	c, ok := parseReleaseVersion(current)
	if !ok {
		return false
	}
	return compareReleaseVersions(l, c) > 0
}

func parseReleaseVersion(raw string) (parsedReleaseVersion, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if s == "" || strings.HasSuffix(s, "-dirty") || gitDescribeReleasePattern.MatchString(s) {
		return parsedReleaseVersion{}, false
	}
	if strings.ContainsRune(s, '+') {
		return parsedReleaseVersion{}, false
	}
	var pre []string
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		prerelease := s[dash+1:]
		pre = strings.Split(prerelease, ".")
		if len(pre) != 2 || !validReleaseStage(pre[0]) || !validNumericIdentifier(pre[1]) {
			return parsedReleaseVersion{}, false
		}
		s = s[:dash]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return parsedReleaseVersion{}, false
	}
	version := parsedReleaseVersion{pre: pre}
	for i, part := range parts {
		if !validNumericIdentifier(part) {
			return parsedReleaseVersion{}, false
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parsedReleaseVersion{}, false
		}
		version.core[i] = value
	}
	return version, true
}

func validNumericIdentifier(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validReleaseStage(value string) bool {
	switch value {
	case "alpha", "beta", "rc":
		return true
	default:
		return false
	}
}

func compareReleaseVersions(a, b parsedReleaseVersion) int {
	for i := range a.core {
		if a.core[i] < b.core[i] {
			return -1
		}
		if a.core[i] > b.core[i] {
			return 1
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		av, aNumeric := numericPrerelease(a.pre[i])
		bv, bNumeric := numericPrerelease(b.pre[i])
		switch {
		case aNumeric && bNumeric && av < bv:
			return -1
		case aNumeric && bNumeric && av > bv:
			return 1
		case aNumeric && !bNumeric:
			return -1
		case !aNumeric && bNumeric:
			return 1
		case !aNumeric && !bNumeric && a.pre[i] < b.pre[i]:
			return -1
		case !aNumeric && !bNumeric && a.pre[i] > b.pre[i]:
			return 1
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

func numericPrerelease(value string) (uint64, bool) {
	if !validNumericIdentifier(value) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil
}
