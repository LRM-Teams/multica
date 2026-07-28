package metrics

import (
	"net"
	"os"
	"strings"
)

// DefaultMetricsAddr is the loopback scrape endpoint used when METRICS_ADDR is
// unset. Production docker historically left METRICS_ADDR empty, which disabled
// HTTP histograms and made p95 alerts impossible. Default on so served latency
// is always recorded; set METRICS_DISABLED=1 to opt out.
const DefaultMetricsAddr = "127.0.0.1:9091"

type Config struct {
	Addr string
	// Disabled is true when METRICS_DISABLED=1 (or true/yes).
	Disabled bool
}

func ConfigFromEnv() Config {
	if metricsDisabledFromEnv() {
		return Config{Disabled: true}
	}
	addr := strings.TrimSpace(os.Getenv("METRICS_ADDR"))
	if addr == "" {
		addr = DefaultMetricsAddr
	}
	return Config{Addr: addr}
}

func metricsDisabledFromEnv() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("METRICS_DISABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c Config) Enabled() bool {
	return !c.Disabled && strings.TrimSpace(c.Addr) != ""
}

func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		host = strings.TrimSpace(addr)
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
