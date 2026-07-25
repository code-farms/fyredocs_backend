package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestNewParserRejectsGarbage(t *testing.T) {
	if _, err := NewParser([]string{"not-an-ip"}); err == nil {
		t.Error("expected error for invalid IP entry")
	}
	if _, err := NewParser([]string{"10.0.0.0/99"}); err == nil {
		t.Error("expected error for invalid CIDR entry")
	}
	if _, err := NewParser([]string{"", "  ", "127.0.0.1", "172.18.0.0/16", "::1"}); err != nil {
		t.Errorf("expected valid list to parse, got %v", err)
	}
}

func TestClientIP(t *testing.T) {
	p, err := NewParser([]string{"172.18.0.0/16", "127.0.0.1", "::1"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			// Direct connection from an untrusted peer: XFF is attacker-supplied.
			name:       "untrusted peer ignores spoofed XFF",
			remoteAddr: "203.0.113.7:5000",
			xff:        "1.2.3.4, 5.6.7.8",
			want:       "203.0.113.7",
		},
		{
			// Caddy (172.18.x) appends the real client to whatever the client sent:
			// rightmost-untrusted picks the appended entry, not the spoof.
			name:       "trusted peer picks rightmost untrusted",
			remoteAddr: "172.18.0.5:443",
			xff:        "9.9.9.9, 8.8.8.8, 198.51.100.23",
			want:       "198.51.100.23",
		},
		{
			name:       "trusted peer single real entry",
			remoteAddr: "172.18.0.5:443",
			xff:        "198.51.100.23",
			want:       "198.51.100.23",
		},
		{
			// Trusted hop appended between client and edge is skipped.
			name:       "skips trusted hops right-to-left",
			remoteAddr: "172.18.0.5:443",
			xff:        "198.51.100.23, 172.18.0.9",
			want:       "198.51.100.23",
		},
		{
			name:       "all trusted falls back to leftmost",
			remoteAddr: "172.18.0.5:443",
			xff:        "172.18.0.2, 172.18.0.3",
			want:       "172.18.0.2",
		},
		{
			name:       "trusted peer with empty XFF returns peer",
			remoteAddr: "172.18.0.5:443",
			xff:        "",
			want:       "172.18.0.5",
		},
		{
			// Malformed entry poisons everything left of it.
			name:       "malformed entry falls back to peer",
			remoteAddr: "172.18.0.5:443",
			xff:        "198.51.100.23, not-an-ip",
			want:       "172.18.0.5",
		},
		{
			name:       "ipv6 client through trusted proxy",
			remoteAddr: "[::1]:9000",
			xff:        "2001:db8::1",
			want:       "2001:db8::1",
		},
		{
			name:       "xff entries with ports are stripped",
			remoteAddr: "172.18.0.5:443",
			xff:        "198.51.100.23:61234",
			want:       "198.51.100.23",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := p.ClientIP(r); got != tc.want {
				t.Errorf("ClientIP(remote=%s, xff=%q) = %q, want %q", tc.remoteAddr, tc.xff, got, tc.want)
			}
		})
	}
}

func TestClientIPNoTrustedProxies(t *testing.T) {
	p, err := NewParser(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:443"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	// Nothing is trusted → the peer itself is the client; XFF ignored.
	if got := p.ClientIP(r); got != "172.18.0.5" {
		t.Errorf("got %q, want peer", got)
	}
}
