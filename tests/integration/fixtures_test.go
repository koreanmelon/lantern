//go:build integration

package integration

import (
	"testing"

	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

func record(name string, typ dnsmessage.Type, body dnsmessage.ResourceBody) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{
			Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET, TTL: 300,
		},
		Body: body,
	}
}

func newTestZone(t *testing.T, origin string, extra ...dnsmessage.Resource) *zone.Zone {
	t.Helper()
	records := []dnsmessage.Resource{
		record(origin, dnsmessage.TypeSOA, &dnsmessage.SOAResource{
			NS: dnsmessage.MustNewName("ns." + origin), MBox: dnsmessage.MustNewName("hostmaster." + origin),
			Serial: 1, Refresh: 3600, Retry: 600, Expire: 86400, MinTTL: 60,
		}),
		record(origin, dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns." + origin)}),
	}
	z, err := zone.NewZone(origin, append(records, extra...))
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func testQuery(t *testing.T) dnsmessage.Message {
	t.Helper()
	return dnsmessage.Message{
		ID: 42, RecursionDesired: true,
		Questions: []dnsmessage.Question{{
			Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
		}},
	}
}
