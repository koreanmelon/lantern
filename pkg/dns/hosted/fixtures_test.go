package hosted

import (
	"testing"

	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

func record(name string, typ dnsmessage.Type, body dnsmessage.ResourceBody) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET, TTL: 300},
		Body:   body,
	}
}

func baseRecords(origin string) []dnsmessage.Resource {
	return []dnsmessage.Resource{
		record(origin, dnsmessage.TypeSOA, &dnsmessage.SOAResource{
			NS: dnsmessage.MustNewName("ns." + origin), MBox: dnsmessage.MustNewName("hostmaster." + origin),
			Serial: 1, Refresh: 3600, Retry: 600, Expire: 86400, MinTTL: 60,
		}),
		record(origin, dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns." + origin)}),
	}
}

func newTestZone(t *testing.T, origin string, extra ...dnsmessage.Resource) *zone.Zone {
	t.Helper()
	z, err := zone.NewZone(origin, append(baseRecords(origin), extra...))
	if err != nil {
		t.Fatal(err)
	}
	return z
}
