package metrics

import "testing"

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:9090", true},
		{"localhost:9090", true},
		{"[::1]:9090", true},
		{":9090", false},
		{"0.0.0.0:9090", false},
		{"10.0.0.5:9090", false},
		{"metrics.example.com:9090", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := IsLoopbackAddr(tt.addr); got != tt.want {
				t.Fatalf("IsLoopbackAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestConfigFromEnvDefaultsLoopback(t *testing.T) {
	t.Setenv("METRICS_ADDR", "")
	t.Setenv("METRICS_DISABLED", "")
	cfg := ConfigFromEnv()
	if !cfg.Enabled() {
		t.Fatal("expected enabled by default")
	}
	if cfg.Addr != DefaultMetricsAddr {
		t.Fatalf("addr=%q want %q", cfg.Addr, DefaultMetricsAddr)
	}
}

func TestConfigFromEnvDisabled(t *testing.T) {
	t.Setenv("METRICS_DISABLED", "1")
	t.Setenv("METRICS_ADDR", "127.0.0.1:1")
	cfg := ConfigFromEnv()
	if cfg.Enabled() {
		t.Fatal("expected disabled")
	}
}
