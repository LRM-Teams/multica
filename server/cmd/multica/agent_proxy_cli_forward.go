package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon"
)

// forwardAgentProxyCLI repairs command resolution when a provider executes a
// login shell that rebuilds PATH and resolves the installed multica binary
// instead of the launch-pinned Agent Proxy wrapper. The environment variable
// contains only that wrapper path; proxy credentials remain wrapper-scoped.
func forwardAgentProxyCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) (bool, int, error) {
	wrapperPath := strings.TrimSpace(os.Getenv(daemon.AgentProxyCLIWrapperEnv))
	if wrapperPath == "" {
		return false, 0, nil
	}
	if !filepath.IsAbs(wrapperPath) {
		return true, 1, fmt.Errorf("%s must be an absolute path", daemon.AgentProxyCLIWrapperEnv)
	}
	info, err := os.Stat(wrapperPath)
	if err != nil {
		return true, 1, fmt.Errorf("Agent Proxy CLI wrapper is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return true, 1, errors.New("Agent Proxy CLI wrapper must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return true, 1, fmt.Errorf("Agent Proxy CLI wrapper is not executable")
	}

	commandName := wrapperPath
	commandArgs := append([]string(nil), args...)
	if runtime.GOOS == "windows" {
		commandName = strings.TrimSpace(os.Getenv("ComSpec"))
		if commandName == "" {
			commandName = "cmd.exe"
		}
		commandArgs = append([]string{"/d", "/s", "/c", wrapperPath}, commandArgs...)
	}
	command := exec.Command(commandName, commandArgs...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = environmentWithoutKey(os.Environ(), daemon.AgentProxyCLIWrapperEnv)
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return true, exitErr.ExitCode(), nil
		}
		return true, 1, fmt.Errorf("run Agent Proxy CLI wrapper: %w", err)
	}
	return true, 0, nil
}

func environmentWithoutKey(environment []string, key string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
