package dns

import (
	"context"
	"fmt"
	"sort"

	"golang.org/x/net/dns/dnsmessage"
)

// zoneSnapshot is an immutable, validated zone collection. A query retains one
// collection even if the authoritative handler publishes a replacement.
type zoneSnapshot struct {
	zones []*Zone
}

func newZoneSnapshot(zones []*Zone) (*zoneSnapshot, error) {
	snapshot := &zoneSnapshot{zones: append([]*Zone(nil), zones...)}
	seen := make(map[string]bool)
	for _, zone := range snapshot.zones {
		if zone == nil || zone.origin == "" {
			return nil, fmt.Errorf("uninitialized zone")
		}
		if seen[zone.origin] {
			return nil, fmt.Errorf("duplicate zone %s", zone.origin)
		}
		seen[zone.origin] = true
	}
	sort.Slice(snapshot.zones, func(i, j int) bool {
		return len(snapshot.zones[i].origin) > len(snapshot.zones[j].origin)
	})
	for _, zone := range snapshot.zones {
		if err := zone.validateTargets(func(name string) bool {
			targetZone := snapshot.zoneFor(name)
			return targetZone != nil && len(targetZone.records[name][dnsmessage.TypeCNAME]) != 0
		}); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func (s *zoneSnapshot) zoneFor(name string) *Zone {
	if s == nil {
		return nil
	}
	for _, zone := range s.zones {
		if withinZone(name, zone.origin) {
			return zone
		}
	}
	return nil
}

func (s *zoneSnapshot) lookup(ctx context.Context, name string, typ dnsmessage.Type) (dnsmessage.Message, error) {
	if s.zoneFor(name) == nil {
		return dnsmessage.Message{Header: dnsmessage.Header{RCode: dnsmessage.RCodeRefused}}, nil
	}
	response := dnsmessage.Message{Header: dnsmessage.Header{Authoritative: true}}
	visited := make(map[string]bool)
	for links := 0; ; links++ {
		if err := ctx.Err(); err != nil {
			return dnsmessage.Message{}, err
		}
		zone := s.zoneFor(name)
		if zone == nil {
			// The alias leaves our authority. Its target is not resolved here.
			return response, nil
		}
		if visited[name] {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			return response, nil
		}
		visited[name] = true
		sets := zone.records[name]
		if records := sets[typ]; len(records) != 0 {
			response.Answers = append(response.Answers, records...)
			return response, nil
		}
		aliases := sets[dnsmessage.TypeCNAME]
		if len(aliases) == 0 {
			zone.setNegativeAnswer(name, &response)
			return response, nil
		}
		if links >= 16 {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			return response, nil
		}
		response.Answers = append(response.Answers, aliases...)
		var err error
		name, err = canonicalName(aliases[0].Body.(*dnsmessage.CNAMEResource).CNAME.String())
		if err != nil {
			return dnsmessage.Message{}, err
		}
	}
}

func (z *Zone) setNegativeAnswer(name string, response *dnsmessage.Message) {
	if _, exists := z.names[name]; !exists {
		response.RCode = dnsmessage.RCodeNameError
	}
	soa := z.soa
	soa.Header.TTL = min(soa.Header.TTL, soa.Body.(*dnsmessage.SOAResource).MinTTL)
	response.Authorities = []dnsmessage.Resource{soa}
}
