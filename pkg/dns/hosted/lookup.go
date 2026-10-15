package hosted

import (
	"context"
	"fmt"
	"sort"

	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

func prepareZones(zones []*zone.Zone) ([]*zone.Zone, error) {
	zones = append([]*zone.Zone(nil), zones...)
	seen := make(map[string]bool)
	for _, z := range zones {
		if z == nil || z.Origin() == "" {
			return nil, fmt.Errorf("uninitialized zone")
		}
		if seen[z.Origin()] {
			return nil, fmt.Errorf("duplicate zone %s", z.Origin())
		}
		if err := z.Validate(); err != nil {
			return nil, err
		}
		seen[z.Origin()] = true
	}
	sort.Slice(zones, func(i, j int) bool {
		return len(zones[i].Origin()) > len(zones[j].Origin())
	})
	return zones, nil
}

func zoneFor(zones []*zone.Zone, name string) *zone.Zone {
	for _, zone := range zones {
		if zone.Has(name) {
			return zone
		}
	}
	return nil
}

func lookup(ctx context.Context, zones []*zone.Zone, name string, typ dnsmessage.Type) (dnsmessage.Message, error) {
	if zoneFor(zones, name) == nil {
		return dnsmessage.Message{RCode: dnsmessage.RCodeRefused}, nil
	}
	response := dnsmessage.Message{Authoritative: true}
	visited := make(map[string]bool)
	for links := 0; ; links++ {
		if err := ctx.Err(); err != nil {
			return dnsmessage.Message{}, err
		}
		z := zoneFor(zones, name)
		if z == nil {
			// The alias leaves our authority. Its target is not resolved here.
			return response, nil
		}
		if visited[name] {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			return response, nil
		}
		visited[name] = true
		records, exists := z.Lookup(name, typ)
		if len(records) != 0 {
			response.Answers = append(response.Answers, records...)
			return response, nil
		}
		aliases, _ := z.Lookup(name, dnsmessage.TypeCNAME)
		if len(aliases) == 0 {
			setNegativeAnswer(z, exists, &response)
			return response, nil
		}
		if links >= 16 {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			return response, nil
		}
		response.Answers = append(response.Answers, aliases...)
		var err error
		name, err = zone.FQDN(aliases[0].Body.(*dnsmessage.CNAMEResource).CNAME.String())
		if err != nil {
			return dnsmessage.Message{}, err
		}
	}
}

func setNegativeAnswer(z *zone.Zone, exists bool, response *dnsmessage.Message) {
	if !exists {
		response.RCode = dnsmessage.RCodeNameError
	}
	soa := z.SOA()
	soa.Header.TTL = min(soa.Header.TTL, soa.Body.(*dnsmessage.SOAResource).MinTTL)
	response.Authorities = []dnsmessage.Resource{soa}
}
