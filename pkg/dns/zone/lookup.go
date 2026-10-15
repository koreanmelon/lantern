package zone

import (
	"context"
	"fmt"
	"sort"

	"golang.org/x/net/dns/dnsmessage"
)

func prepareZones(zones []*Zone) ([]*Zone, error) {
	zones = append([]*Zone(nil), zones...)
	seen := make(map[string]bool)
	for _, zone := range zones {
		if zone == nil || zone.origin == "" {
			return nil, fmt.Errorf("uninitialized zone")
		}
		if seen[zone.origin] {
			return nil, fmt.Errorf("duplicate zone %s", zone.origin)
		}
		seen[zone.origin] = true
	}
	sort.Slice(zones, func(i, j int) bool {
		return len(zones[i].origin) > len(zones[j].origin)
	})
	return zones, nil
}

func zoneFor(zones []*Zone, name string) *Zone {
	for _, zone := range zones {
		if zone.Has(name) {
			return zone
		}
	}
	return nil
}

func lookup(ctx context.Context, zones []*Zone, name string, typ dnsmessage.Type) (dnsmessage.Message, error) {
	if zoneFor(zones, name) == nil {
		return dnsmessage.Message{Header: dnsmessage.Header{RCode: dnsmessage.RCodeRefused}}, nil
	}
	response := dnsmessage.Message{Header: dnsmessage.Header{Authoritative: true}}
	visited := make(map[string]bool)
	for links := 0; ; links++ {
		if err := ctx.Err(); err != nil {
			return dnsmessage.Message{}, err
		}
		zone := zoneFor(zones, name)
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
		if records := zone.records[RecordKey{name: name, typ: typ}]; len(records) != 0 {
			response.Answers = append(response.Answers, records...)
			return response, nil
		}
		aliases := zone.records[RecordKey{name: name, typ: dnsmessage.TypeCNAME}]
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
		name, err = fqdn(aliases[0].Body.(*dnsmessage.CNAMEResource).CNAME.String())
		if err != nil {
			return dnsmessage.Message{}, err
		}
	}
}

func (z *Zone) setNegativeAnswer(name string, response *dnsmessage.Message) {
	if !z.names[name] {
		response.RCode = dnsmessage.RCodeNameError
	}
	soa := z.soa
	soa.Header.TTL = min(soa.Header.TTL, soa.Body.(*dnsmessage.SOAResource).MinTTL)
	response.Authorities = []dnsmessage.Resource{soa}
}
