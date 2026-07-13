//go:build !windows

package agent

// Keep synthesized chat prompts out of argv. Linux limits each exec argument
// to roughly 128 KiB even when ARG_MAX is larger, while Pi already accepts the
// initial non-interactive prompt from stdin on every supported platform.
func piPromptViaStdin() bool { return true }
