package dns

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
