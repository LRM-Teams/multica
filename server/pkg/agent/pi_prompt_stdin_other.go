//go:build !windows

package agent

func piPromptViaStdin() bool { return false }
