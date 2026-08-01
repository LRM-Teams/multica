package doubaodialog

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveDuplexSessionCreate(t *testing.T) {
	if strings.TrimSpace(os.Getenv("DOUBAO_DIALOG_LIVE")) == "" {
		t.Skip("set DOUBAO_DIALOG_LIVE=1 and DOUBAO_DIALOG_API_KEY to run live duplex smoke")
	}
	cfg := ConfigFromEnv()
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session, err := client.OpenSession(ctx, DefaultSessionConfig(
		cfg.Model,
		cfg.Voice,
		DefaultDialogInstructions(),
		[]Tool{MulticaDelegateTool()},
	))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close(context.Background())

	event, err := session.Recv(ctx)
	if err != nil {
		t.Fatalf("recv: %v (logid=%s)", err, session.LogID())
	}
	if event.Type != EventSessionCreated {
		t.Fatalf("first event type = %q want %s (logid=%s raw=%s)", event.Type, EventSessionCreated, session.LogID(), string(event.Raw))
	}
	if event.SessionID == "" {
		t.Fatalf("session.created missing session.id (logid=%s)", session.LogID())
	}
	t.Logf("live duplex session ok id=%s logid=%s", event.SessionID, session.LogID())
}
