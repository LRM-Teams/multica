package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterLocalControlRoutesPreservesRouteOwnership(t *testing.T) {
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerLocalControlRoutes(mux)

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{name: "inbox", method: http.MethodGet, path: "/inbox", want: "/inbox"},
		{name: "inbox ack", method: http.MethodPost, path: "/inbox/ack", want: "/inbox/ack"},
		{name: "message check", method: http.MethodPost, path: "/credential-proxy/messages/check", want: "POST /credential-proxy/messages/check"},
		{name: "message read", method: http.MethodPost, path: "/credential-proxy/messages/read", want: "POST /credential-proxy/messages/read"},
		{name: "message send", method: http.MethodPost, path: "/credential-proxy/messages/send", want: "POST /credential-proxy/messages/send"},
		{name: "message search", method: http.MethodPost, path: "/credential-proxy/messages/search", want: "POST /credential-proxy/messages/search"},
		{name: "message resolve", method: http.MethodPost, path: "/credential-proxy/messages/resolve", want: "POST /credential-proxy/messages/resolve"},
		{name: "message react", method: http.MethodPost, path: "/credential-proxy/messages/react", want: "POST /credential-proxy/messages/react"},
		{name: "agent api", method: http.MethodGet, path: "/api/agent/messages/read", want: "/api/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, pattern := mux.Handler(httptest.NewRequest(test.method, test.path, nil))
			if pattern != test.want {
				t.Fatalf("route pattern = %q, want %q", pattern, test.want)
			}
		})
	}
}
