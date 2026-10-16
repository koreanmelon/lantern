package webui

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestRecordFormats(t *testing.T) {
	for _, tc := range []struct{ typ, name, value string }{
		{"A", "app", "192.0.2.1"}, {"AAAA", "app", "2001:db8::1"},
		{"CNAME", "alias", "app"}, {"NS", "@", "ns.home.arpa."},
		{"PTR", "ptr", "app.home.arpa."}, {"MX", "@", "10 mail.home.arpa."},
		{"SRV", "_web._tcp", "0 5 443 app.home.arpa."},
		{"TXT", "text", "hello world"}, {"TXT", "empty", ""},
		{"TXT", "chunks", `["hello", "world", "line\nbreak", "[bracket]"]`},
		{"TXT", "newline", "hello\nworld"},
		{"SOA", "@", "ns.home.arpa. hostmaster.home.arpa. 1 3600 600 86400 60"},
	} {
		t.Run(tc.typ+"/"+tc.name, func(t *testing.T) {
			form := recordForm{Name: tc.name, Type: tc.typ, TTL: "60", Value: tc.value}
			record, err := parseRecord(form, "home.arpa.")
			if err != nil {
				t.Fatal(err)
			}
			form.Name, form.Type, form.Value = record.Header.Name.String(), typeName(record.Header.Type), recordValue(record)
			roundtrip, err := parseRecord(form, "home.arpa.")
			if err != nil {
				t.Fatal(err)
			}
			first := dnsmessage.Message{Answers: []dnsmessage.Resource{record}}
			second := dnsmessage.Message{Answers: []dnsmessage.Resource{roundtrip}}
			a, err := first.Pack()
			if err != nil {
				t.Fatal(err)
			}
			b, err := second.Pack()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("record format did not roundtrip: %q", form.Value)
			}
		})
	}
}

func TestInvalidRecordFormats(t *testing.T) {
	for _, tc := range []struct{ typ, ttl, value string }{
		{"A", "60", "::1"}, {"AAAA", "60", "192.0.2.1"}, {"AAAA", "60", "fe80::1%eth0"},
		{"A", "-1", "192.0.2.1"}, {"A", "2147483648", "192.0.2.1"},
		{"MX", "60", "65536 mail"}, {"MX", "60", "10"},
		{"SRV", "60", "0 1 65536 app"}, {"SRV", "60", "0 app"},
		{"TXT", "60", "[]"}, {"TXT", "60", "[1]"}, {"TXT", "60", strings.Repeat("a", 256)},
		{"SOA", "60", "ns hostmaster 4294967296 1 1 1 1"}, {"SOA", "60", "ns hostmaster 1"},
		{"CNAME", "60", ""}, {"NOPE", "60", "value"},
	} {
		t.Run(tc.typ+"/"+tc.ttl+"/"+tc.value, func(t *testing.T) {
			if _, err := parseRecord(recordForm{Name: "app", Type: tc.typ, TTL: tc.ttl, Value: tc.value}, "home.arpa."); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}
