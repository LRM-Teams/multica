//go:build !windows

package cli

// CleanupStaleUpdateArtifacts is a no-op on Unix — there are no sidecar files
// to reclaim.
func CleanupStaleUpdateArtifacts() {}
