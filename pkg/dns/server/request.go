package dnsserver

import (
	"net"
	"net/netip"

	"golang.org/x/net/dns/dnsmessage"
)

// Request is a decoded DNS query and its immediate network peer's IP address.
// ClientIP is invalid when unavailable. IPv4-mapped IPv6 addresses are unmapped.
// It is not an authenticated identity and does not account for forwarded headers.
type Request struct {
	Message  dnsmessage.Message
	ClientIP netip.Addr
}

func clientIP(address net.Addr) netip.Addr {
	switch address := address.(type) {
	case *net.UDPAddr:
		if address != nil {
			return address.AddrPort().Addr().Unmap()
		}
	case *net.TCPAddr:
		if address != nil {
			return address.AddrPort().Addr().Unmap()
		}
	default:
		if address != nil {
			host, _, err := net.SplitHostPort(address.String())
			if err == nil {
				ip, err := netip.ParseAddr(host)
				if err == nil {
					return ip.Unmap()
				}
			}
		}
	}
	return netip.Addr{}
}
