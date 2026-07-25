// Package clientip derives the real client IP for net/http services running
// behind reverse proxies, using a trusted-proxy list and the rightmost-
// untrusted X-Forwarded-For algorithm. Gin services get the equivalent from
// gin's SetTrustedProxies; this package exists for the non-Gin api-gateway.
//
// Why rightmost-untrusted: each proxy hop APPENDS the peer it saw to
// X-Forwarded-For, so only the entries appended by our own (trusted) proxies
// are trustworthy. Walking right-to-left and skipping trusted addresses, the
// first untrusted entry is the closest address a trusted proxy actually
// observed — everything left of it is client-supplied and spoofable.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Parser resolves client IPs against a set of trusted proxy networks.
type Parser struct {
	trusted []*net.IPNet
}

// NewParser builds a Parser from a list of CIDRs or bare IPs (which become
// /32 or /128). Empty entries are skipped; a malformed entry is an error so a
// typo in TRUSTED_PROXIES fails loudly at boot instead of silently changing
// rate-limit behavior.
func NewParser(entries []string) (*Parser, error) {
	p := &Parser{}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			ip := net.ParseIP(entry)
			if ip == nil {
				return nil, fmt.Errorf("clientip: invalid trusted proxy IP %q", entry)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			p.trusted = append(p.trusted, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, ipnet, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("clientip: invalid trusted proxy CIDR %q: %w", entry, err)
		}
		p.trusted = append(p.trusted, ipnet)
	}
	return p, nil
}

// ClientIP returns the originating client IP for r. When the direct peer
// (RemoteAddr) is not a trusted proxy, the peer IS the client and any
// X-Forwarded-For content is ignored (it is attacker-supplied). When the peer
// is trusted, X-Forwarded-For is walked right-to-left skipping trusted
// entries; the first untrusted entry wins. If every entry is trusted the
// leftmost survives as a best-effort fallback.
func (p *Parser) ClientIP(r *http.Request) string {
	peer := remoteIP(r)
	if peer == "" {
		return r.RemoteAddr
	}
	if !p.isTrusted(net.ParseIP(peer)) {
		return peer
	}

	entries := splitXFF(r.Header.Get("X-Forwarded-For"))
	for i := len(entries) - 1; i >= 0; i-- {
		ip := net.ParseIP(entries[i])
		if ip == nil {
			// A malformed hop poisons everything to its left — stop here and
			// fall back to the trusted peer rather than trusting garbage.
			break
		}
		if !p.isTrusted(ip) {
			return entries[i]
		}
		if i == 0 {
			// All entries trusted: the leftmost is the best remaining signal.
			return entries[0]
		}
	}
	return peer
}

func (p *Parser) isTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range p.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteIP strips the port from RemoteAddr; RemoteAddr without a port is
// returned as-is.
func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// splitXFF splits an X-Forwarded-For header into trimmed, non-empty entries,
// stripping any :port suffixes some proxies append.
func splitXFF(header string) []string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		// Handle "ip:port" (IPv4) and "[v6]:port" forms.
		if host, _, err := net.SplitHostPort(entry); err == nil && net.ParseIP(host) != nil {
			entry = host
		}
		entry = strings.Trim(entry, "[]")
		out = append(out, entry)
	}
	return out
}
