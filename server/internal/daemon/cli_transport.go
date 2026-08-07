package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/turntransport"
)

// prepareStableAgentCLITransport returns the fixed agent-scoped transport that
// D4/D6 consume once one-active-turn-per-agent serialization is live. D3 keeps
// the existing per-run path below in production: publishing a single current
// envelope before the serialization gate would cross-bind concurrent turns.
func prepareStableAgentCLITransport(cfg Config, workspaceID, agentID, multicaBin string) (*turntransport.Transport, error) {
	if workspaceID == "" || agentID == "" {
		return nil, fmt.Errorf("workspace_id and agent_id are required")
	}
	root := filepath.Join(agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID), "runtime", "cli-transport")
	return turntransport.Prepare(root, multicaBin)
}

// stripProviderCredentialTransport removes raw credential transport keys from a
// process-identity environment. Credentials bind via request.Token → Bind /
// CLI wrapper (MULTICA_TOKEN_FILE only inside the wrapper), never in the
// provider process environment. Call before splitAgentProcessEnvironment so
// legacy agentEnv maps that still carry TOKEN_FILE do not fail D3 closed.
func stripProviderCredentialTransport(environment map[string]string) map[string]string {
	if environment == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(environment))
	for key, value := range environment {
		switch key {
		case "MULTICA_TOKEN", "MULTICA_TOKEN_FILE", turntransport.EnvelopePathEnv:
			continue
		default:
			out[key] = value
		}
	}
	return out
}

// splitAgentProcessEnvironment is the stable-process/current-turn contract for
// persistent runtimes. It is intentionally a daemon seam so D4 does not grow a
// second, provider-specific classification.
// Credential transport keys must already be stripped (see
// stripProviderCredentialTransport); SplitEnvironment still fail-closes if any remain.
func splitAgentProcessEnvironment(environment map[string]string) (stable, currentTurn map[string]string, err error) {
	return turntransport.SplitEnvironment(environment)
}

func prepareTaskCLITransport(cfg Config, workspaceID, agentID, runID, multicaBin, token string) (string, string, error) {
	if workspaceID == "" || agentID == "" || runID == "" {
		return "", "", fmt.Errorf("workspace_id, agent_id, and run_id are required")
	}
	if strings.TrimSpace(multicaBin) == "" {
		return "", "", fmt.Errorf("multica binary path is required")
	}
	if token == "" {
		return "", "", fmt.Errorf("task token is required")
	}

	root := filepath.Join(agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID), "runtime", "cli-transport", runID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", fmt.Errorf("create cli transport dir: %w", err)
	}

	tokenFile := filepath.Join(root, "token")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		return "", "", fmt.Errorf("write cli token file: %w", err)
	}
	if err := os.Chmod(tokenFile, 0o600); err != nil {
		return "", "", fmt.Errorf("chmod cli token file: %w", err)
	}

	wrapper := filepath.Join(root, turntransport.CliWrapperFilename())
	body := cliWrapperBody(tokenFile, multicaBin)
	if err := os.WriteFile(wrapper, []byte(body), 0o700); err != nil {
		return "", "", fmt.Errorf("write cli wrapper: %w", err)
	}
	if err := os.Chmod(wrapper, 0o700); err != nil {
		return "", "", fmt.Errorf("chmod cli wrapper: %w", err)
	}

	return root, tokenFile, nil
}

// cliWrapperBody returns the credential CLI wrapper for the per-task cli
// transport. POSIX gets a #!/bin/sh shim; Windows gets a .cmd batch (see
// turntransport.CliWrapperFilename) so ShellExecute never opens the bare
// extensionless file and pops the "How do you want to open this file?" dialog.
func cliWrapperBody(tokenFile, multicaBin string) string {
	if runtime.GOOS == "windows" {
		return windowsCLIWrapperBody(tokenFile, multicaBin)
	}
	return "#!/bin/sh\n" +
		"unset MULTICA_TOKEN\n" +
		"export MULTICA_TOKEN_FILE=" + shellQuote(tokenFile) + "\n" +
		"exec " + shellQuote(multicaBin) + " \"$@\"\n"
}

// windowsCLIWrapperBody emits a cmd.exe batch wrapper (see cliWrapperBody). No
// bare extensionless shim is ever written on Windows, so ShellExecute cannot
// open the "How do you want to open this file?" (选择应用的打开方式) dialog.
func windowsCLIWrapperBody(tokenFile, multicaBin string) string {
	var body strings.Builder
	body.WriteString("@echo off\r\n")
	body.WriteString("set \"MULTICA_TOKEN=\"\r\n")
	body.WriteString("set \"MULTICA_TOKEN_FILE=" + tokenFile + "\"\r\n")
	body.WriteString("call \"" + multicaBin + "\" %*\r\n")
	body.WriteString("exit /b %ERRORLEVEL%\r\n")
	return body.String()
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
