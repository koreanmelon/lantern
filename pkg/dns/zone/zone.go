package zone

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

// Zone stores and answers queries for one DNS zone. Its methods are safe for
// concurrent use. Record bodies and returned data are borrowed and must remain
// read-only. A Zone must not be copied after first use. Construct it with NewZone;
// its zero value refuses queries and cannot accept records.
type Zone struct {
	mu       sync.RWMutex
	origin   string
	soa      dnsmessage.Resource
	records  map[RecordKey][]dnsmessage.Resource
	names    map[string]bool
	revision uint64
}

// NewZone validates and indexes records without cloning their bodies or slices.
// Names must be absolute (end in a dot). The apex must contain exactly one SOA
// and at least one NS record. Callers must not mutate supplied record data.
func NewZone(origin string, records []dnsmessage.Resource) (*Zone, error) {
	name, err := FQDN(origin)
	if err != nil {
		return nil, fmt.Errorf("zone origin: %w", err)
	}

	z := &Zone{
		origin:   name,
		revision: 1,
		records:  make(map[RecordKey][]dnsmessage.Resource),
		names:    make(map[string]bool),
	}
	for i, record := range records {
		if err := z.add(record); err != nil {
			return nil, fmt.Errorf("zone %s record %d: %w", name, i, err)
		}
	}

	if err := z.validate(); err != nil {
		return nil, err
	}

	z.soa = z.records[RecordKey{name: name, typ: dnsmessage.TypeSOA}][0]

	return z, nil
}

// Add atomically adds a record after validating the complete resulting zone.
// Failure leaves the zone unchanged. The record body is borrowed, not cloned.
func (z *Zone) Add(record dnsmessage.Resource) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.origin == "" {
		return fmt.Errorf("uninitialized zone")
	}
	var records []dnsmessage.Resource
	for _, set := range z.records {
		records = append(records, set...)
	}
	records = append(records, record)
	replacement, err := NewZone(z.origin, records)
	if err != nil {
		return err
	}
	z.records, z.names, z.soa = replacement.records, replacement.names, replacement.soa
	z.revision++
	return nil
}

// ReplaceRecords atomically replaces all records while keeping the origin fixed.
// The replacement must contain a valid SOA and NS set. Validation happens before
// the write lock; failure leaves the zone unchanged. Concurrent replacements are
// safe, with the last assignment winning. Supplied record bodies remain borrowed.
func (z *Zone) ReplaceRecords(records []dnsmessage.Resource) error {
	if z.origin == "" {
		return fmt.Errorf("uninitialized zone")
	}
	replacement, err := NewZone(z.origin, records)
	if err != nil {
		return err
	}
	z.mu.Lock()
	z.records, z.names, z.soa = replacement.records, replacement.names, replacement.soa
	z.revision++
	z.mu.Unlock()
	return nil
}

// add is used only during private construction, before the zone is published.
func (z *Zone) add(record dnsmessage.Resource) error {
	owner, err := FQDN(record.Header.Name.String())
	if err != nil {
		return err
	}

	if !z.Has(owner) {
		return fmt.Errorf("owner %s is outside the zone", owner)
	}
	if slices.Contains(strings.Split(owner, "."), "*") {
		return fmt.Errorf("owner %s: wildcard records are unsupported", owner)
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
		if _, err := FQDN(target.String()); err != nil {
			return fmt.Errorf("owner %s: invalid target: %w", owner, err)
		}
	}
	if owner != z.origin && (record.Header.Type == dnsmessage.TypeSOA || record.Header.Type == dnsmessage.TypeNS) {
		return fmt.Errorf("owner %s: SOA/NS must be at the apex; delegation is unsupported", owner)
	}
	// Check wire-format validity without decoding or cloning the record body.
	message := dnsmessage.Message{Answers: []dnsmessage.Resource{record}}
	if _, err := message.Pack(); err != nil {
		return fmt.Errorf("owner %s: %w", owner, err)
	}
	if len(z.records[RecordKey{name: owner, typ: dnsmessage.TypeCNAME}]) != 0 {
		return fmt.Errorf("owner %s: CNAME must be the only record", owner)
	}
	if record.Header.Type == dnsmessage.TypeCNAME {
		for key := range z.records {
			if key.name == owner {
				return fmt.Errorf("owner %s: CNAME must be the only record", owner)
			}
		}
	}
	key := RecordKey{name: owner, typ: record.Header.Type}
	set := z.records[key]
	if record.Header.Type == dnsmessage.TypeSOA && len(set) != 0 {
		return fmt.Errorf("owner %s: multiple SOA records", owner)
	}
	if len(set) != 0 && set[0].Header.TTL != record.Header.TTL {
		return fmt.Errorf("owner %s: inconsistent RRset TTL", owner)
	}
	z.records[key] = append(set, record)
	// Index empty non-terminals as well as record owners.
	for name := owner; ; {
		z.names[name] = true
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

func (z *Zone) Validate() error {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.validate()
}

func (z *Zone) validate() error {
	soa := z.records[RecordKey{name: z.origin, typ: dnsmessage.TypeSOA}]
	ns := z.records[RecordKey{name: z.origin, typ: dnsmessage.TypeNS}]
	if len(soa) != 1 || len(ns) == 0 {
		return fmt.Errorf("zone %s requires one apex SOA and at least one apex NS", z.origin)
	}

	for key, records := range z.records {
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
			name, _ := FQDN(target.String()) // Already validated by Add.
			if len(z.records[RecordKey{name: name, typ: dnsmessage.TypeCNAME}]) != 0 {
				return fmt.Errorf("owner %s: NS/MX/SRV target %s must not be a CNAME", key.name, name)
			}
		}
	}

	return nil
}

func (z *Zone) Has(name string) bool {
	return z.origin != "" && (z.origin == "." || name == z.origin || strings.HasSuffix(name, "."+z.origin))
}

// Origin returns the zone's canonical absolute origin.
func (z *Zone) Origin() string {
	return z.origin
}

// Lookup returns the RRset for name and whether the name exists, including empty
// intermediate names. Names are absolute and matched case-insensitively. Invalid
// names return no records and false. The returned records and bodies are borrowed
// and must not be mutated.
func (z *Zone) Lookup(name string, typ dnsmessage.Type) ([]dnsmessage.Resource, bool) {
	name, err := FQDN(name)
	if err != nil {
		return nil, false
	}
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.records[RecordKey{name, typ}], z.names[name]
}

// SOA returns the zone's SOA record. Its body is borrowed and must not be mutated.
func (z *Zone) SOA() dnsmessage.Resource {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.soa
}
