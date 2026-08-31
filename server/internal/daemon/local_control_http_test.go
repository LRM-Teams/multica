package daemon

import (
	"net/http/httptest"
	"testing"
)

func TestLocalControlAuthorizedRequiresMatchingConfiguredToken(t *testing.T) {
	d := &Daemon{cfg: Config{LocalControlToken: " control-token "}}

	tests := []struct {
		name   string
		header string
		wantOK bool
	}{
		{name: "matching token", header: "control-token", wantOK: true},
		{name: "trimmed matching token", header: "  control-token  ", wantOK: true},
		{name: "wrong token", header: "other-token", wantOK: false},
		{name: "missing token", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://localhost/internal/control", nil)
			if tt.header != "" {
				req.Header.Set("X-Multica-Control-Token", tt.header)
			}
			if got := d.localControlAuthorized(req); got != tt.wantOK {
				t.Fatalf("localControlAuthorized() = %v, want %v", got, tt.wantOK)
			}
		})
	}
}

func TestLocalControlAuthorizedFailsClosedWithoutConfiguredToken(t *testing.T) {
	d := &Daemon{}
	req := httptest.NewRequest("POST", "http://localhost/internal/control", nil)
	req.Header.Set("X-Multica-Control-Token", "control-token")

	if d.localControlAuthorized(req) {
		t.Fatal("localControlAuthorized() accepted a token without configuration")
	}
}
