package dnsserver

import (
	"net"
	"net/netip"
	"testing"
)

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		address net.Addr
		want    string
	}{
		{"UDP IPv4", &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 53}, "192.0.2.1"},
		{"TCP IPv4", &net.TCPAddr{IP: net.ParseIP("192.0.2.2"), Port: 12345}, "192.0.2.2"},
		{"UDP IPv6", &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 53}, "2001:db8::1"},
		{"TCP IPv6", &net.TCPAddr{IP: net.ParseIP("2001:db8::2"), Port: 53}, "2001:db8::2"},
		{"scoped IPv6", &net.UDPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 53}, "fe80::1%eth0"},
		{"mapped IPv4", &net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.3"), Port: 53}, "192.0.2.3"},
		{"text address", testAddr("[::ffff:192.0.2.4]:1234"), "192.0.2.4"},
		{"nil", nil, ""},
		{"typed nil UDP", (*net.UDPAddr)(nil), ""},
		{"typed nil TCP", (*net.TCPAddr)(nil), ""},
		{"empty IP", &net.UDPAddr{}, ""},
		{"non IP", testAddr("pipe"), ""},
		{"non IP hostname", testAddr("example.com:53"), ""},
		{"malformed address", testAddr("[::1"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := netip.Addr{}
			if tc.want != "" {
				want = netip.MustParseAddr(tc.want)
			}
			if got := clientIP(tc.address); got != want {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}
