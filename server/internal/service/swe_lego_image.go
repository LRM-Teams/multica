package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SweLegoCacheKey derives the docker image cache key for a SWE-Lego image.
// The key is a function of (repo_url, base_commit, issue_date, base_image) —
// the four inputs that determine the image contents. Repeated issues against
// the same quadruple reuse the image (spec §4.3).
//
// The pipe-delimited preimage mirrors how the areal side would derive the
// same key if it ever needed to reference an image by content; do not change
// the delimiter without coordinating both sides.
func SweLegoCacheKey(repoURL, baseCommit, issueDate, baseImage string) string {
	preimage := repoURL + "|" + baseCommit + "|" + issueDate + "|" + baseImage
	h := sha256.Sum256([]byte(preimage))
	return hex.EncodeToString(h[:])
}

// sweLegoImageRef returns the docker image ref tagged with the cache key.
func sweLegoImageRef(cacheKey string) string {
	return "swe-lego:" + cacheKey
}

// SweLegoBuildScript returns the shell script run on a Fleet build-node to
// produce a SWE-Lego docker image. The script:
//  1. Clones the repo shallow-extended to base_commit.
//  2. SWE-Lego anti-hacking: deletes git history after issue_date via
//     `git filter-repo --commit-cutoff`, so an agent cannot git log or
//     git blame its way to the future fix (spec §2 decision 5, §4.3).
//  3. docker builds the image tagged with the cache key.
//
// The script is shipped to the node via /api/v1/nodes/exec and run there; the
// multica server never shells out to docker locally (spec §2 decision 8).
func SweLegoBuildScript(repoURL, baseCommit, issueDate, baseImage, cacheKey string) string {
	imageRef := sweLegoImageRef(cacheKey)
	var b strings.Builder
	fmt.Fprintf(&b, "set -euo pipefail\n")
	fmt.Fprintf(&b, "rm -rf /tmp/swe-lego-build && mkdir -p /tmp/swe-lego-build\n")
	fmt.Fprintf(&b, "cd /tmp/swe-lego-build\n")
	fmt.Fprintf(&b, "git clone --filter=blob:none %s repo\n", shellQuote(repoURL))
	fmt.Fprintf(&b, "cd repo\n")
	fmt.Fprintf(&b, "git fetch origin %s\n", shellQuote(baseCommit))
	fmt.Fprintf(&b, "git checkout %s\n", shellQuote(baseCommit))
	// SWE-Lego anti-hacking: find the last commit at or before issue_date,
	// then physically delete everything after it.
	fmt.Fprintf(&b, "cutoff_commit=$(git rev-list -1 --before=%s HEAD)\n", shellQuote(issueDate))
	fmt.Fprintf(&b, "git filter-repo --replace-ref refs/heads/main:${cutoff_commit} --commit-cutoff ${cutoff_commit}\n")
	fmt.Fprintf(&b, "pip install -e . 2>/dev/null || true\n")
	fmt.Fprintf(&b, "docker build -t %s -f /tmp/swe-lego-build/Dockerfile .\n", shellQuote(imageRef))
	return b.String()
}

// shellQuote single-quotes a string for safe inclusion in a shell script.
// Single quotes prevent shell expansion of repo URLs / commit hashes that
// might contain characters with special meaning.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
