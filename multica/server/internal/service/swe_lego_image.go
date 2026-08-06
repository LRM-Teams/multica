package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"
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
//  2. SWE-Lego anti-hacking: runs git filter-repo with a commit-callback
//     that drops every commit whose committer date is after issue_date,
//     so an agent inside the container cannot git log or git blame its
//     way to the future fix (spec §2 decision 5, §4.3).
//  3. docker builds the image tagged with the cache key.
//
// The script is shipped to the node via /api/v1/nodes/exec and run there; the
// multica server never shells out to docker locally (spec §2 decision 8).
//
// Returns an error if issueDate is not valid RFC3339.
func SweLegoBuildScript(repoURL, baseCommit, issueDate, baseImage, cacheKey string) (string, error) {
	imageRef := sweLegoImageRef(cacheKey)
	issueTime, err := time.Parse(time.RFC3339, issueDate)
	if err != nil {
		return "", fmt.Errorf("parse issue_date %q as RFC3339: %w", issueDate, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "set -euo pipefail\n")
	fmt.Fprintf(&b, "rm -rf /tmp/swe-lego-build && mkdir -p /tmp/swe-lego-build\n")
	fmt.Fprintf(&b, "cd /tmp/swe-lego-build\n")
	fmt.Fprintf(&b, "git clone --filter=blob:none %s repo\n", shellQuote(repoURL))
	fmt.Fprintf(&b, "cd repo\n")
	fmt.Fprintf(&b, "git fetch origin %s\n", shellQuote(baseCommit))
	fmt.Fprintf(&b, "git checkout %s\n", shellQuote(baseCommit))
	// SWE-Lego anti-hacking: drop every commit whose committer date is after
	// issue_date. git-filter-repo rewrites all refs, expires the reflog, and
	// runs `git gc --prune=now` at the end, so the orphaned commits are
	// physically removed from the object database — an agent inside the
	// container cannot git log or git blame its way to the future fix
	// (spec §2 decision 5, §4.3).
	//
	// commit.committer_date is the raw git bytes b'<unix_ts> <tz>', so we
	// parse issueDate to a Unix timestamp in Go and compare as integers in
	// the callback. Lexicographic byte comparison would not work because
	// b'<unix_ts> ...' starts with '1' (current era) and ISO 8601 starts
	// with '2', so the predicate would always be false.
	callback := fmt.Sprintf("if int(commit.committer_date.split()[0]) > %d: commit.skip()", issueTime.Unix())
	fmt.Fprintf(&b, "git filter-repo --force --commit-callback %s\n", shellQuote(callback))
	fmt.Fprintf(&b, "pip install -e . 2>/dev/null || true\n")
	// Write the Dockerfile, then build.
	dockerfile, err := SweLegoDockerfile(baseImage)
	if err != nil {
		return "", fmt.Errorf("render dockerfile: %w", err)
	}
	fmt.Fprintf(&b, "cat > /tmp/swe-lego-build/Dockerfile <<'EOF'\n%s\nEOF\n", dockerfile)
	fmt.Fprintf(&b, "docker build -t %s -f /tmp/swe-lego-build/Dockerfile .\n", shellQuote(imageRef))
	return b.String(), nil
}

// sweLegoDockerfileTmpl is the Dockerfile baked into each SWE-Lego image.
// The daemon binary is built from the existing multica daemon source and
// copied in at image-build time — it is the same binary that runs locally
// today, just inside the container (spec §4.3).
const sweLegoDockerfileTmpl = `FROM {{.BaseImage}}
COPY repo/ /workspace/repo
WORKDIR /workspace/repo
RUN pip install -e . 2>/dev/null || true
COPY multica-daemon /usr/local/bin/multica-daemon
ENV MULTICA_DAEMON_AUTO_REGISTER=1
CMD ["multica-daemon", "run"]
`

// SweLegoDockerfile renders the Dockerfile for a SWE-Lego image given the
// base image. The build script writes this to /tmp/swe-lego-build/Dockerfile
// before invoking docker build.
func SweLegoDockerfile(baseImage string) (string, error) {
	t, err := template.New("swe-lego-dockerfile").Parse(sweLegoDockerfileTmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, struct{ BaseImage string }{BaseImage: baseImage}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// shellQuote single-quotes a string for safe inclusion in a shell script.
// Single quotes prevent shell expansion of repo URLs / commit hashes that
// might contain characters with special meaning.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ErrSweLegoBuildFailed is returned when the build script exits non-zero.
var ErrSweLegoBuildFailed = errors.New("swe-lego image build failed")

// BuildOrReuse returns the docker image ref and the node it lives on.
// If the image is already cached on the picked node (cache hit), the build
// is short-circuited. Otherwise the build script is shipped to the node via
// NodeExec.Exec. The image always lives on the node that built it (spec §4.3
// "Image locality") — registry-backed distribution is out of scope for v1.
func BuildOrReuse(ctx context.Context, exec NodeExec, repoURL, baseCommit, issueDate, baseImage string) (imageRef string, nodeID string, err error) {
	node, err := exec.PickBuildNode(ctx)
	if err != nil {
		return "", "", fmt.Errorf("pick build node: %w", err)
	}
	cacheKey := SweLegoCacheKey(repoURL, baseCommit, issueDate, baseImage)
	ref := sweLegoImageRef(cacheKey)

	// 1. Cache check: `docker image inspect` exits 0 if the image exists.
	_, exitCode, err := exec.Exec(ctx, node, []string{"docker", "image", "inspect", ref})
	if err != nil {
		return "", "", fmt.Errorf("cache inspect transport error: %w", err)
	}
	if exitCode == 0 {
		return ref, node, nil
	}

	// 2. Cache miss: ship the build script and run it on the node.
	script, err := SweLegoBuildScript(repoURL, baseCommit, issueDate, baseImage, cacheKey)
	if err != nil {
		return "", "", fmt.Errorf("build script: %w", err)
	}
	out, exitCode, err := exec.Exec(ctx, node, []string{"sh", "-c", script})
	if err != nil {
		return "", "", fmt.Errorf("build transport error: %w", err)
	}
	if exitCode != 0 {
		return "", "", fmt.Errorf("swe-lego build failed: exit %d: %s: %w", exitCode, strings.TrimSpace(out), ErrSweLegoBuildFailed)
	}
	return ref, node, nil
}
