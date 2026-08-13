package computer

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const controlTokenFile = "machine-upgrade-control.token"

// ControlTokenPath is the owner-only local mutation credential shared by the
// Computer lifecycle commands and the resident. It authenticates loopback
// control; server authorization remains a separate check inside the resident.
func ControlTokenPath(profile string) string {
	return filepath.Join(RootDir(profile), controlTokenFile)
}

// EnsureControlToken creates one durable per-owner secret with restrictive
// permissions, or returns the existing secret.
func EnsureControlToken(profile string) (string, error) {
	path := ControlTokenPath(profile)
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

// ReadControlToken reads the existing credential without creating local state.
func ReadControlToken(profile string) (string, error) {
	data, err := os.ReadFile(ControlTokenPath(profile))
	if err != nil {
		return "", err
	}
	if token := strings.TrimSpace(string(data)); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("local machine control token is empty")
}
