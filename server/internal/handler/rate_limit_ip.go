package handler

import (
	"net/http"
	"net/netip"
	"strings"
)

// clientIPForRateLimit returns the IP used as a rate-limit bucket key.
//
// Default behaviour: use the host portion of r.RemoteAddr. Forwarded
// headers (X-Forwarded-For, X-Real-IP) are IGNORED unless the operator
// has explicitly opted in via MULTICA_TRUSTED_PROXIES — and even then
// only when r.RemoteAddr is itself inside one of the listed CIDRs.
func (h *Handler) clientIPForRateLimit(r *http.Request) string {
	remoteIP := remoteAddrHost(r.RemoteAddr)
	if len(h.cfg.TrustedProxies) == 0 {
		return remoteIP
	}
	remoteAddr, ok := parseNetIPAddr(remoteIP)
	if !ok || !addrInPrefixes(remoteAddr, h.cfg.TrustedProxies) {
		// Source isn't a trusted proxy — headers can't be believed.
		return remoteIP
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return remoteIP
}

func remoteAddrHost(remote string) string {
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(remote, "[") {
		if end := strings.IndexByte(remote, ']'); end > 0 {
			return remote[1:end]
		}
	}
	if i := strings.LastIndexByte(remote, ':'); i >= 0 && !strings.Contains(remote, "]") {
		if strings.Count(remote, ":") == 1 {
			return remote[:i]
		}
	}
	return remote
}

func parseNetIPAddr(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func addrInPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
