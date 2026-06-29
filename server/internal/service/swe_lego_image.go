package service

import (
	"crypto/sha256"
	"encoding/hex"
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
