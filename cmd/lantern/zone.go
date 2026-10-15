package main

import (
	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

// exampleHandler keeps the initial zone explicit in Go until configuration
// loading is added. The address is the intended Pi LAN address, not discovery.
func exampleHandler() (*zone.Zone, error) {
	apex := dnsmessage.MustNewName("home.arpa.")
	server := dnsmessage.MustNewName("lantern.home.arpa.")
	return zone.NewZone("home.arpa.", []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{Name: apex, Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET, TTL: 300},
			Body: &dnsmessage.SOAResource{
				NS: server, MBox: dnsmessage.MustNewName("hostmaster.home.arpa."),
				Serial: 1, Refresh: 3600, Retry: 600, Expire: 86400, MinTTL: 60,
			},
		},
		{
			Header: dnsmessage.ResourceHeader{Name: apex, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET, TTL: 300},
			Body:   &dnsmessage.NSResource{NS: server},
		},
		{
			Header: dnsmessage.ResourceHeader{Name: server, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.AResource{A: [4]byte{192, 168, 1, 2}},
		},
		{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("pi.home.arpa."), Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.CNAMEResource{CNAME: server},
		},
	})
}
