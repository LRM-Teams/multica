package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/computer"
)

const machineUpgradeControlTokenFile = "machine-upgrade-control.token"

func machineUpgradeControlTokenPath(profile string) string {
	return filepath.Join(computer.RootDir(profile), machineUpgradeControlTokenFile)
}

// ensureMachineUpgradeControlToken creates a per-profile secret readable only
// by the owning user. This is transport authentication, not server identity:
// the daemon still applies the canonical server authorization on its request.
func ensureMachineUpgradeControlToken(profile string) (string, error) {
	path := machineUpgradeControlTokenPath(profile)
	if existing, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(existing))
		if token == "" {
			return "", fmt.Errorf("local machine control token is empty")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("restrict local machine control token: %w", err)
		}
		return token, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create local machine control directory: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate local machine control token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write local machine control token: %w", err)
	}
	return token, nil
}

func readMachineUpgradeControlToken(profile string) (string, error) {
	data, err := os.ReadFile(machineUpgradeControlTokenPath(profile))
	if err != nil {
		return "", err
	}
	if token := strings.TrimSpace(string(data)); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("local machine control token is empty")
}
