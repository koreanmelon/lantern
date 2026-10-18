package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

var recordTypes = []string{"A", "AAAA", "CNAME", "MX", "NS", "PTR", "SOA", "SRV", "TXT"}

type recordForm struct {
	Name, Type, TTL, Value  string
	Revision, Action, Title string
}

func absoluteName(value, origin string) (dnsmessage.Name, error) {
	value = strings.TrimSpace(value)
	if value == "@" {
		value = origin
	}
	if value == "" {
		return dnsmessage.Name{}, fmt.Errorf("a name is required; use @ for the zone apex")
	}
	if !safeName(value) {
		return dnsmessage.Name{}, fmt.Errorf("names must be valid UTF-8 without whitespace or control characters")
	}
	if !strings.HasSuffix(value, ".") {
		if origin == "." {
			value += "."
		} else {
			value += "." + origin
		}
	}
	name, err := zone.FQDN(value)
	if err != nil {
		return dnsmessage.Name{}, err
	}
	return dnsmessage.NewName(name)
}

func safeName(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func number(value, field string, bits int) (uint64, error) {
	n, err := strconv.ParseUint(value, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned %d-bit integer", field, bits)
	}
	return n, nil
}

func parseRecord(form recordForm, origin string) (dnsmessage.Resource, error) {
	if !utf8.ValidString(form.Name) || !utf8.ValidString(form.Value) {
		return dnsmessage.Resource{}, fmt.Errorf("record names and values must be valid UTF-8")
	}
	name, err := absoluteName(form.Name, origin)
	if err != nil {
		return dnsmessage.Resource{}, fmt.Errorf("name: %w", err)
	}
	ttl, err := number(strings.TrimSpace(form.TTL), "TTL", 32)
	if err != nil {
		return dnsmessage.Resource{}, err
	}
	if ttl > 1<<31-1 {
		return dnsmessage.Resource{}, fmt.Errorf("TTL must be between 0 and 2147483647 seconds")
	}
	r := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: uint32(ttl)}}
	value := strings.TrimSpace(form.Value)
	fields := strings.Fields(value)
	need := func(n int) error {
		if len(fields) != n {
			return fmt.Errorf("%s requires %d space-separated values; see the record format guide", form.Type, n)
		}
		return nil
	}
	switch form.Type {
	case "A", "AAAA":
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" {
			return r, fmt.Errorf("enter a valid unscoped IP address")
		}
		if form.Type == "A" {
			if !ip.Is4() {
				return r, fmt.Errorf("A records require an IPv4 address")
			}
			r.Header.Type, r.Body = dnsmessage.TypeA, &dnsmessage.AResource{A: ip.As4()}
		} else {
			if !ip.Is6() {
				return r, fmt.Errorf("AAAA records require an IPv6 address")
			}
			r.Header.Type, r.Body = dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: ip.As16()}
		}
	case "CNAME", "NS", "PTR":
		if err := need(1); err != nil {
			return r, err
		}
		target, err := absoluteName(value, origin)
		if err != nil {
			return r, fmt.Errorf("target: %w", err)
		}
		switch form.Type {
		case "CNAME":
			r.Header.Type, r.Body = dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: target}
		case "NS":
			r.Header.Type, r.Body = dnsmessage.TypeNS, &dnsmessage.NSResource{NS: target}
		case "PTR":
			r.Header.Type, r.Body = dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: target}
		}
	case "MX":
		if err := need(2); err != nil {
			return r, err
		}
		preference, err := number(fields[0], "preference", 16)
		if err != nil {
			return r, err
		}
		target, err := absoluteName(fields[1], origin)
		if err != nil {
			return r, err
		}
		r.Header.Type, r.Body = dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: uint16(preference), MX: target}
	case "SRV":
		if err := need(4); err != nil {
			return r, err
		}
		var values [3]uint16
		for i, field := range []string{"priority", "weight", "port"} {
			n, err := number(fields[i], field, 16)
			if err != nil {
				return r, err
			}
			values[i] = uint16(n)
		}
		target, err := absoluteName(fields[3], origin)
		if err != nil {
			return r, err
		}
		r.Header.Type, r.Body = dnsmessage.TypeSRV, &dnsmessage.SRVResource{Priority: values[0], Weight: values[1], Port: values[2], Target: target}
	case "TXT":
		chunks := []string{form.Value}
		if strings.HasPrefix(value, "[") {
			var raw []json.RawMessage
			if err := json.Unmarshal([]byte(value), &raw); err != nil || len(raw) == 0 {
				return r, fmt.Errorf("TXT must be plain text or a non-empty JSON array of strings")
			}
			chunks = make([]string, len(raw))
			for i, item := range raw {
				if bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
					return r, fmt.Errorf("TXT array items must be strings, not null")
				}
				if err := json.Unmarshal(item, &chunks[i]); err != nil {
					return r, fmt.Errorf("TXT array items must be strings")
				}
			}
		}
		for _, chunk := range chunks {
			if len(chunk) > 255 {
				return r, fmt.Errorf("each TXT string must be at most 255 bytes; use a JSON array for multiple strings")
			}
		}
		r.Header.Type, r.Body = dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: chunks}
	case "SOA":
		if err := need(7); err != nil {
			return r, err
		}
		ns, err := absoluteName(fields[0], origin)
		if err != nil {
			return r, err
		}
		mailbox, err := absoluteName(fields[1], origin)
		if err != nil {
			return r, err
		}
		var values [5]uint32
		for i, field := range []string{"serial", "refresh", "retry", "expire", "minimum TTL"} {
			n, err := number(fields[i+2], field, 32)
			if err != nil {
				return r, err
			}
			values[i] = uint32(n)
		}
		r.Header.Type, r.Body = dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: ns, MBox: mailbox, Serial: values[0], Refresh: values[1], Retry: values[2], Expire: values[3], MinTTL: values[4]}
	default:
		return r, fmt.Errorf("unsupported record type %q", form.Type)
	}
	return r, nil
}

func typeName(typ dnsmessage.Type) string {
	return strings.TrimPrefix(typ.String(), "Type")
}

func recordValue(r dnsmessage.Resource) string {
	switch body := r.Body.(type) {
	case *dnsmessage.AResource:
		return netip.AddrFrom4(body.A).String()
	case *dnsmessage.AAAAResource:
		return netip.AddrFrom16(body.AAAA).String()
	case *dnsmessage.CNAMEResource:
		return body.CNAME.String()
	case *dnsmessage.NSResource:
		return body.NS.String()
	case *dnsmessage.PTRResource:
		return body.PTR.String()
	case *dnsmessage.MXResource:
		return fmt.Sprintf("%d %s", body.Pref, body.MX.String())
	case *dnsmessage.SRVResource:
		return fmt.Sprintf("%d %d %d %s", body.Priority, body.Weight, body.Port, body.Target.String())
	case *dnsmessage.TXTResource:
		if len(body.TXT) == 1 && !strings.HasPrefix(strings.TrimSpace(body.TXT[0]), "[") && !strings.ContainsAny(body.TXT[0], "\x00\r\n") {
			return body.TXT[0]
		}
		value, _ := json.Marshal(body.TXT)
		return string(value)
	case *dnsmessage.SOAResource:
		return fmt.Sprintf("%s %s %d %d %d %d %d", body.NS.String(), body.MBox.String(), body.Serial, body.Refresh, body.Retry, body.Expire, body.MinTTL)
	}
	return ""
}
