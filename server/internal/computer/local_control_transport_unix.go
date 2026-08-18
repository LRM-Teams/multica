//go:build !windows

package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const unixControlPathLimit = 100

func localControlEndpoint(residentRoot, name string) string {
	filename := "service.sock"
	if name != "service" {
		digest := sha256.Sum256([]byte(name))
		filename = "runner-" + hex.EncodeToString(digest[:8]) + ".sock"
	}
	path := filepath.Join(localControlSocketDir(residentRoot), filename)
	if len(path) > unixControlPathLimit {
		digest := sha256.Sum256([]byte(strings.TrimSpace(residentRoot)))
		path = filepath.Join(os.TempDir(), fmt.Sprintf("multica-%d-%s", os.Getuid(), hex.EncodeToString(digest[:8])), filename)
		if len(path) > unixControlPathLimit {
			path = filepath.Join("/tmp", fmt.Sprintf("multica-%d-%s", os.Getuid(), hex.EncodeToString(digest[:8])), filename)
		}
	}
	return "unix://" + filepath.ToSlash(path)
}

func validPlatformLocalControlEndpoint(endpoint string) bool {
	path := unixControlPath(endpoint)
	return filepath.IsAbs(path) && len(path) <= unixControlPathLimit
}

func listenLocalControl(endpoint string) (net.Listener, error) {
	path := unixControlPath(endpoint)
	if !filepath.IsAbs(path) || len(path) > unixControlPathLimit {
		return nil, fmt.Errorf("Unix control socket path is invalid or too long")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create local control directory: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale local control socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("protect local control socket: %w", err)
	}
	return &removingUnixListener{Listener: listener, path: path}, nil
}

func dialLocalControl(ctx context.Context, endpoint string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", unixControlPath(endpoint))
}

func unixControlPath(endpoint string) string {
	return strings.TrimPrefix(strings.TrimSpace(endpoint), "unix://")
}

type removingUnixListener struct {
	net.Listener
	path string
}

func (listener *removingUnixListener) Close() error {
	err := listener.Listener.Close()
	removeErr := os.Remove(listener.path)
	if err != nil {
		return err
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}
