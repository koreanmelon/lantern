package zone

import (
	"fmt"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// Zone is an immutable IN-class zone. Construct it with NewZone.
// Wildcards, delegations, and DNSSEC records are not supported.
type Zone struct {
	origin  string
	soa     dnsmessage.Resource
	records map[string]map[dnsmessage.Type][]dnsmessage.Resource
	names   map[string]struct{}
}

// NewZone validates and copies records. Names must be absolute (end in a dot).
// The apex must contain exactly one SOA and at least one NS record.
func NewZone(origin string, records []dnsmessage.Resource) (*Zone, error) {
	name, err := canonicalName(origin)
	if err != nil {
		return nil, fmt.Errorf("zone origin: %w", err)
	}
	z := &Zone{origin: name, records: make(map[string]map[dnsmessage.Type][]dnsmessage.Resource), names: make(map[string]struct{})}
	for i, record := range records {
		if err := z.add(record); err != nil {
			return nil, fmt.Errorf("zone %s record %d: %w", name, i, err)
		}
	}
	apex := z.records[name]
	if len(apex[dnsmessage.TypeSOA]) != 1 || len(apex[dnsmessage.TypeNS]) == 0 {
		return nil, fmt.Errorf("zone %s requires one apex SOA and at least one apex NS", name)
	}
	z.soa = apex[dnsmessage.TypeSOA][0]
	if err := z.validateTargets(func(name string) bool { return len(z.records[name][dnsmessage.TypeCNAME]) != 0 }); err != nil {
		return nil, err
	}
	return z, nil
}

func (z *Zone) add(record dnsmessage.Resource) error {
	owner, err := canonicalName(record.Header.Name.String())
	if err != nil {
		return err
	}
	if !withinZone(owner, z.origin) {
		return fmt.Errorf("owner %s is outside the zone", owner)
	}
	for _, label := range strings.Split(owner, ".") {
		if label == "*" {
			return fmt.Errorf("owner %s: wildcard records are unsupported", owner)
		}
	}
	if record.Header.Class != dnsmessage.ClassINET {
		return fmt.Errorf("owner %s: only IN records are supported", owner)
	}
	// Each supported body's nil check also rejects typed-nil interface values.
	validBody := false
	switch body := record.Body.(type) {
	case *dnsmessage.AResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeA
	case *dnsmessage.AAAAResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeAAAA
	case *dnsmessage.NSResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeNS
	case *dnsmessage.SOAResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeSOA
	case *dnsmessage.CNAMEResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeCNAME
	case *dnsmessage.MXResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeMX
	case *dnsmessage.TXTResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeTXT
	case *dnsmessage.SRVResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypeSRV
	case *dnsmessage.PTRResource:
		validBody = body != nil && record.Header.Type == dnsmessage.TypePTR
	}
	if !validBody {
		return fmt.Errorf("owner %s: unsupported or mismatched record body", owner)
	}
	var targets []dnsmessage.Name
	switch body := record.Body.(type) {
	case *dnsmessage.NSResource:
		targets = []dnsmessage.Name{body.NS}
	case *dnsmessage.SOAResource:
		targets = []dnsmessage.Name{body.NS, body.MBox}
	case *dnsmessage.CNAMEResource:
		targets = []dnsmessage.Name{body.CNAME}
	case *dnsmessage.MXResource:
		targets = []dnsmessage.Name{body.MX}
	case *dnsmessage.SRVResource:
		targets = []dnsmessage.Name{body.Target}
	case *dnsmessage.PTRResource:
		targets = []dnsmessage.Name{body.PTR}
	}
	for _, target := range targets {
		if _, err := canonicalName(target.String()); err != nil {
			return fmt.Errorf("owner %s: invalid target: %w", owner, err)
		}
	}
	if owner != z.origin && (record.Header.Type == dnsmessage.TypeSOA || record.Header.Type == dnsmessage.TypeNS) {
		return fmt.Errorf("owner %s: SOA/NS must be at the apex; delegation is unsupported", owner)
	}
	// Packing and unpacking validates the wire representation and gives the zone
	// its own copy of pointer/slice-backed resource bodies (including TXT data).
	copy, err := copyMessage(dnsmessage.Message{Answers: []dnsmessage.Resource{record}})
	if err != nil {
		return fmt.Errorf("owner %s: %w", owner, err)
	}
	record = copy.Answers[0]
	sets := z.records[owner]
	if sets == nil {
		sets = make(map[dnsmessage.Type][]dnsmessage.Resource)
		z.records[owner] = sets
	}
	if len(sets[dnsmessage.TypeCNAME]) != 0 || (record.Header.Type == dnsmessage.TypeCNAME && len(sets) != 0) {
		return fmt.Errorf("owner %s: CNAME must be the only record", owner)
	}
	set := sets[record.Header.Type]
	if record.Header.Type == dnsmessage.TypeSOA && len(set) != 0 {
		return fmt.Errorf("owner %s: multiple SOA records", owner)
	}
	if len(set) != 0 && set[0].Header.TTL != record.Header.TTL {
		return fmt.Errorf("owner %s: inconsistent RRset TTL", owner)
	}
	sets[record.Header.Type] = append(set, record)
	// Index empty non-terminals as well as record owners.
	for name := owner; ; {
		z.names[name] = struct{}{}
		if name == z.origin {
			break
		}
		name = name[strings.IndexByte(name, '.')+1:]
		if name == "" {
			name = "."
		}
	}
	return nil
}

func canonicalName(name string) (string, error) {
	if name == "." {
		return name, nil
	}
	if !strings.HasSuffix(name, ".") || len(name) > 254 {
		return "", fmt.Errorf("invalid absolute DNS name %q", name)
	}
	for _, label := range strings.Split(name[:len(name)-1], ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("invalid or unsupported label in %q", name)
		}
	}
	// DNS case folding is ASCII-only, not Unicode case conversion.
	result := []byte(name)
	for i, b := range result {
		if b >= 'A' && b <= 'Z' {
			result[i] = b + ('a' - 'A')
		}
	}
	return string(result), nil
}

func (z *Zone) validateTargets(isAlias func(string) bool) error {
	for owner, sets := range z.records {
		for _, records := range sets {
			for _, record := range records {
				var target dnsmessage.Name
				switch body := record.Body.(type) {
				case *dnsmessage.NSResource:
					target = body.NS
				case *dnsmessage.MXResource:
					target = body.MX
				case *dnsmessage.SRVResource:
					target = body.Target
				default:
					continue
				}
				name, _ := canonicalName(target.String()) // Already validated by add.
				if isAlias(name) {
					return fmt.Errorf("owner %s: NS/MX/SRV target %s must not be a CNAME", owner, name)
				}
			}
		}
	}
	return nil
}

func withinZone(name, origin string) bool {
	return origin == "." || name == origin || strings.HasSuffix(name, "."+origin)
}

func copyMessage(message dnsmessage.Message) (dnsmessage.Message, error) {
	packet, err := message.Pack()
	if err != nil {
		return dnsmessage.Message{}, err
	}
	var result dnsmessage.Message
	if err := result.Unpack(packet); err != nil {
		return dnsmessage.Message{}, err
	}
	return result, nil
}
