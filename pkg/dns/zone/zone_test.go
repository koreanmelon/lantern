package zone

import (
	"fmt"
	"strings"
	"testing"

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

func newTestZone(t *testing.T, origin string, extra ...dnsmessage.Resource) *Zone {
	t.Helper()
	zone, err := NewZone(origin, append(baseRecords(origin), extra...))
	if err != nil {
		t.Fatal(err)
	}
	return zone
}

func TestNewZoneValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func([]dnsmessage.Resource) []dnsmessage.Resource
	}{
		{"missing SOA", func(r []dnsmessage.Resource) []dnsmessage.Resource { return r[1:] }},
		{"missing NS", func(r []dnsmessage.Resource) []dnsmessage.Resource { return r[:1] }},
		{"duplicate SOA", func(r []dnsmessage.Resource) []dnsmessage.Resource { return append(r, r[0]) }},
		{"outside owner", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("nothome.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))
		}},
		{"non IN", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			r[0].Header.Class = dnsmessage.ClassCHAOS
			return r
		}},
		{"nil body", func(r []dnsmessage.Resource) []dnsmessage.Resource { r[0].Body = nil; return r }},
		{"typed nil", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			r[0].Body = (*dnsmessage.SOAResource)(nil)
			return r
		}},
		{"mismatched type", func(r []dnsmessage.Resource) []dnsmessage.Resource { r[0].Header.Type = dnsmessage.TypeA; return r }},
		{"wildcard", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("*.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))
		}},
		{"delegation", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("child.home.arpa.", dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.home.arpa.")}))
		}},
		{"CNAME conflict", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("a.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}), record("a.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("b.home.arpa.")}))
		}},
		{"CNAME conflict reversed", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("a.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("b.home.arpa.")}), record("a.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))
		}},
		{"CNAME conflict case insensitive", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("A.Home.ARPA.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"value"}}), record("a.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("b.home.arpa.")}))
		}},
		{"multiple CNAMEs", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			c := record("a.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("b.home.arpa.")})
			return append(r, c, c)
		}},
		{"RRset TTL mismatch", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			a := record("a.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{})
			b := a
			b.Header.TTL++
			return append(r, a, b)
		}},
		{"bad target", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			r[1].Body = &dnsmessage.NSResource{NS: dnsmessage.MustNewName("relative")}
			return r
		}},
		{"oversized TXT", func(r []dnsmessage.Resource) []dnsmessage.Resource {
			return append(r, record("a.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{strings.Repeat("x", 256)}}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewZone("home.arpa.", tc.modify(baseRecords("home.arpa."))); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	for _, origin := range []string{"", "home.arpa", "bad..arpa.", strings.Repeat("a", 64) + ".arpa."} {
		t.Run("origin="+origin, func(t *testing.T) {
			if _, err := NewZone(origin, nil); err == nil {
				t.Fatal("expected invalid origin")
			}
		})
	}
}

func TestZoneRejectsAliasTargets(t *testing.T) {
	target := dnsmessage.MustNewName("alias.home.arpa.")
	for _, tc := range []struct {
		name   string
		record dnsmessage.Resource
	}{
		{"NS", record("home.arpa.", dnsmessage.TypeNS, &dnsmessage.NSResource{NS: target})},
		{"MX", record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{MX: target})},
		{"SRV", record("_web._tcp.home.arpa.", dnsmessage.TypeSRV, &dnsmessage.SRVResource{Target: target, Port: 443})},
	} {
		for _, aliasFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/aliasFirst=%v", tc.name, aliasFirst), func(t *testing.T) {
				alias := record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("real.home.arpa.")})
				extra := []dnsmessage.Resource{tc.record, alias}
				if aliasFirst {
					extra[0], extra[1] = extra[1], extra[0]
				}
				if _, err := NewZone("home.arpa.", append(baseRecords("home.arpa."), extra...)); err == nil {
					t.Fatal("accepted alias target")
				}
			})
		}
	}
}

func TestRecordKeyIndex(t *testing.T) {
	z := newTestZone(t, "home.arpa.",
		record("APP.Home.ARPA.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}),
		record("app.home.arpa.", dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 1, 0x0d, 0xb8}}),
		record("app.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"app"}}),
		record("other.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 3}}),
	)
	for _, tc := range []struct {
		name  string
		typ   dnsmessage.Type
		count int
	}{
		{"app.home.arpa.", dnsmessage.TypeA, 2},
		{"app.home.arpa.", dnsmessage.TypeAAAA, 1},
		{"app.home.arpa.", dnsmessage.TypeTXT, 1},
		{"other.home.arpa.", dnsmessage.TypeA, 1},
		{"other.home.arpa.", dnsmessage.TypeAAAA, 0},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.typ), func(t *testing.T) {
			records := z.records[RecordKey{name: tc.name, typ: tc.typ}]
			if len(records) != tc.count {
				t.Fatalf("got %d records, want %d", len(records), tc.count)
			}
			for _, record := range records {
				owner, err := FQDN(record.Header.Name.String())
				if err != nil || owner != tc.name || record.Header.Type != tc.typ {
					t.Fatal("RRset contains a record with a different key")
				}
			}
		})
	}
}

func TestCNAMECanHaveDescendants(t *testing.T) {
	// An intermediate name's presence in the name index does not mean it owns
	// any records. CNAME exclusivity concerns only records at the same owner.
	newTestZone(t, "home.arpa.",
		record("child.alias.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}),
		record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("target.home.arpa.")}),
	)
}

func TestZoneLookup(t *testing.T) {
	txt := &dnsmessage.TXTResource{TXT: []string{"hello"}}
	z := newTestZone(t, "HOME.ARPA.",
		record("app.home.arpa.", dnsmessage.TypeTXT, txt),
		record("api.apps.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}),
	)
	for _, tc := range []struct {
		name   string
		typ    dnsmessage.Type
		exists bool
		count  int
	}{
		{"APP.Home.ARPA.", dnsmessage.TypeTXT, true, 1},
		{"app.home.arpa.", dnsmessage.TypeAAAA, true, 0},
		{"apps.home.arpa.", dnsmessage.TypeA, true, 0},
		{"missing.home.arpa.", dnsmessage.TypeA, false, 0},
		{"example.com.", dnsmessage.TypeA, false, 0},
		{"relative", dnsmessage.TypeA, false, 0},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.typ), func(t *testing.T) {
			records, exists := z.Lookup(tc.name, tc.typ)
			if exists != tc.exists || len(records) != tc.count {
				t.Fatalf("got %d records, exists=%v", len(records), exists)
			}
			if len(records) > 0 && records[0].Body != txt {
				t.Fatal("Lookup did not return the borrowed body")
			}
		})
	}
	if z.Origin() != "home.arpa." || z.SOA().Header.Type != dnsmessage.TypeSOA || z.SOA().Header.TTL != 300 {
		t.Fatal("incorrect zone metadata")
	}
}
