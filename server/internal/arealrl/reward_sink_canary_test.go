package arealrl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A29: bridge error bodies can echo a proxy key; durable delivery failures
// must contain the redaction marker instead of that credential.
func TestRewardSinkCanaryRedaction(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	const canary = "sk-live-CANARYKEY123456"
	bridge := &setRewardBridge{status: []int{400}, key: canary}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey))
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "canary", trajectoryID: "t-canary", proxyKey: canary,
		reward: 0.5, status: "pending", nextAt: store.now,
	})
	if _, err := sink.DeliverOnce(context.Background(), 1); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	row := store.row("canary")
	if row.status != "failed" || strings.Contains(row.lastErr, canary) || !strings.Contains(row.lastErr, "[REDACTED]") {
		t.Fatalf("persisted reward failure = %+v", row)
	}
}
