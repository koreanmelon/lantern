package dns

import (
	"context"
	"fmt"
	"sort"

	"golang.org/x/net/dns/dnsmessage"
)

// Authoritative answers only from its configured zones, without recursion or
// forwarding. It is safe for concurrent use. Its zero value refuses all queries.
type Authoritative struct {
	zones []*Zone
}

// NewAuthoritative captures immutable zones, selecting the longest matching
// origin when zones overlap. Uninitialized or duplicate zones are rejected.
func NewAuthoritative(zones ...*Zone) (*Authoritative, error) {
	a := &Authoritative{zones: append([]*Zone(nil), zones...)}
	seen := make(map[string]bool)
	for _, zone := range a.zones {
		if zone == nil || zone.origin == "" {
			return nil, fmt.Errorf("uninitialized zone")
		}
		if seen[zone.origin] {
			return nil, fmt.Errorf("duplicate zone %s", zone.origin)
		}
		seen[zone.origin] = true
	}
	sort.Slice(a.zones, func(i, j int) bool { return len(a.zones[i].origin) > len(a.zones[j].origin) })
	for _, zone := range a.zones {
		if err := zone.validateTargets(func(name string) bool {
			targetZone := a.zoneFor(name)
			return targetZone != nil && len(targetZone.records[name][dnsmessage.TypeCNAME]) != 0
		}); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (a *Authoritative) zoneFor(name string) *Zone {
	for _, zone := range a.zones {
		if withinZone(name, zone.origin) {
			return zone
		}
	}
	return nil
}

// Handle answers a single IN-class question. ANY, transfers, and OPT questions
// are refused. Responses do not share mutable record data with the zone store.
func (a *Authoritative) Handle(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
	if err := ctx.Err(); err != nil {
		return dnsmessage.Message{}, err
	}
	response := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID: query.ID, Response: true, OpCode: query.OpCode,
			RecursionDesired: query.RecursionDesired,
		},
		Questions: query.Questions,
	}
	if query.Response || len(query.Questions) != 1 {
		response.RCode = dnsmessage.RCodeFormatError
		return response, nil
	}
	if query.OpCode != 0 {
		response.RCode = dnsmessage.RCodeNotImplemented
		return response, nil
	}
	question := query.Questions[0]
	if question.Class != dnsmessage.ClassINET || question.Type == dnsmessage.TypeALL ||
		question.Type == dnsmessage.TypeOPT || question.Type == 251 || question.Type == 252 ||
		question.Type == 253 || question.Type == 254 {
		response.RCode = dnsmessage.RCodeRefused
		return response, nil
	}
	name, err := canonicalName(question.Name.String())
	if err != nil {
		response.RCode = dnsmessage.RCodeFormatError
		return response, nil
	}
	if a.zoneFor(name) == nil {
		response.RCode = dnsmessage.RCodeRefused
		return response, nil
	}
	response.Authoritative = true
	visited := make(map[string]bool)
	for links := 0; ; links++ {
		if err := ctx.Err(); err != nil {
			return dnsmessage.Message{}, err
		}
		zone := a.zoneFor(name)
		if zone == nil {
			// The alias leaves our authority. Its target is not resolved here.
			break
		}
		if visited[name] {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			break
		}
		visited[name] = true
		sets := zone.records[name]
		if records := sets[question.Type]; len(records) != 0 {
			response.Answers = append(response.Answers, records...)
			break
		}
		if aliases := sets[dnsmessage.TypeCNAME]; len(aliases) != 0 {
			if links >= 16 {
				response.RCode = dnsmessage.RCodeServerFailure
				response.Answers = nil
				break
			}
			response.Answers = append(response.Answers, aliases...)
			name, err = canonicalName(aliases[0].Body.(*dnsmessage.CNAMEResource).CNAME.String())
			if err != nil {
				return dnsmessage.Message{}, err
			}
			continue
		}
		if _, exists := zone.names[name]; !exists {
			response.RCode = dnsmessage.RCodeNameError
		}
		soa := zone.soa
		soa.Header.TTL = min(soa.Header.TTL, soa.Body.(*dnsmessage.SOAResource).MinTTL)
		response.Authorities = []dnsmessage.Resource{soa}
		break
	}
	return copyMessage(response)
}
