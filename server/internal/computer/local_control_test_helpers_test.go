package computer

import (
	"context"
	"encoding/json"
	"testing"
)

type localControlTestHandler func(context.Context, string, map[string]string, json.RawMessage) (any, error)

func localControlTestServer(t *testing.T, handler localControlTestHandler) string {
	t.Helper()
	registry := NewLocalControlRegistry()
	for _, spec := range localControlOperationSpecs {
		op := spec.Name
		if err := registry.Register(op, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
			return handler(ctx, op, headers, raw)
		}); err != nil {
			t.Fatal(err)
		}
	}
	endpoint := ServiceControlEndpoint(t.TempDir())
	listener, err := ListenLocalControl(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	go ServeLocalControlRPC(ctx, listener, registry)
	t.Cleanup(func() { _ = listener.Close() })
	return endpoint
}
