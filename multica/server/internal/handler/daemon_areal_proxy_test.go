// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestTaskToResponseMapsArealProxyFromContext pins the claim-time wiring for
// Task 6 (§4.4): a trained task whose context carries an `areal_proxy` object
// (written by the session-open hook, Task 5) surfaces on the claim response's
// new ArealProxy field so the daemon can route the runtime through the RL proxy.
func TestTaskToResponseMapsArealProxyFromContext(t *testing.T) {
	ctx := []byte(`{"areal_proxy":{"provider":"areal","model":"areal-default",` +
		`"api_key":"proxy-key-123","base_url":"http://db_bridge_stub:9100/v1",` +
		`"session_id":"task-1-0"}}`)

	resp := taskToResponse(db.AgentInboxEvent{Context: ctx}, "ws-1")

	if resp.ArealProxy == nil {
		t.Fatal("expected ArealProxy populated from context.areal_proxy, got nil")
	}
	if resp.ArealProxy.Provider != "areal" || resp.ArealProxy.Model != "areal-default" {
		t.Errorf("provider/model = %q/%q, want areal/areal-default",
			resp.ArealProxy.Provider, resp.ArealProxy.Model)
	}
	if resp.ArealProxy.APIKey != "proxy-key-123" {
		t.Errorf("api_key = %q, want proxy-key-123", resp.ArealProxy.APIKey)
	}
	if resp.ArealProxy.BaseURL != "http://db_bridge_stub:9100/v1" {
		t.Errorf("base_url = %q, want http://db_bridge_stub:9100/v1", resp.ArealProxy.BaseURL)
	}
}

// TestTaskToResponseNoArealProxyForNormalTask ensures a non-training task is
// never accidentally routed through the proxy: absent / empty / unrelated
// context, and a partially-populated areal_proxy (missing api_key/base_url),
// all yield a nil ArealProxy.
func TestTaskToResponseNoArealProxyForNormalTask(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(`{}`),
		[]byte(`{"other":"value"}`),
		[]byte(`not json`),
		[]byte(`{"areal_proxy":{"provider":"areal"}}`),                // missing api_key + base_url
		[]byte(`{"areal_proxy":{"provider":"areal","api_key":"pk"}}`), // missing base_url
	}
	for _, ctx := range cases {
		resp := taskToResponse(db.AgentInboxEvent{Context: ctx}, "ws-1")
		if resp.ArealProxy != nil {
			t.Errorf("context %q: expected nil ArealProxy, got %+v", ctx, resp.ArealProxy)
		}
	}
}
