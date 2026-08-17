//go:build windows

package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

const namedPipePrefix = `npipe://multica-`

func localControlEndpoint(residentRoot, name string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(residentRoot) + "\x00" + name))
	role := "runner-"
	if name == "service" {
		role = "service-"
	}
	return namedPipePrefix + role + hex.EncodeToString(digest[:16])
}

func validPlatformLocalControlEndpoint(endpoint string) bool {
	name := strings.TrimPrefix(strings.TrimSpace(endpoint), namedPipePrefix)
	return (strings.HasPrefix(name, "service-") || strings.HasPrefix(name, "runner-")) && !strings.ContainsAny(name, `/\\`)
}

func listenLocalControl(endpoint string) (net.Listener, error) {
	return winio.ListenPipe(namedPipePath(endpoint), &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;OW)",
	})
}

func dialLocalControl(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, namedPipePath(endpoint))
}

func namedPipePath(endpoint string) string {
	return `\\.\pipe\` + strings.TrimPrefix(strings.TrimSpace(endpoint), "npipe://")
}
