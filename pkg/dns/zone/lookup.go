package zone

import (
	"context"

	"golang.org/x/net/dns/dnsmessage"
)

// lookup requires the zone read lock for the complete CNAME chain.
func (z *Zone) lookup(ctx context.Context, name string, typ dnsmessage.Type) (dnsmessage.Message, error) {
	if !z.Has(name) {
		return dnsmessage.Message{RCode: dnsmessage.RCodeRefused}, nil
	}
	response := dnsmessage.Message{Authoritative: true}
	visited := make(map[string]bool)
	for links := 0; ; links++ {
		if err := ctx.Err(); err != nil {
			return dnsmessage.Message{}, err
		}
		if !z.Has(name) {
			// The alias leaves our authority. Its target is not resolved here.
			return response, nil
		}
		if visited[name] {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			return response, nil
		}
		visited[name] = true
		records := z.records[RecordKey{name: name, typ: typ}]
		if len(records) != 0 {
			response.Answers = append(response.Answers, records...)
			return response, nil
		}
		aliases := z.records[RecordKey{name: name, typ: dnsmessage.TypeCNAME}]
		if len(aliases) == 0 {
			z.setNegativeAnswer(name, &response)
			return response, nil
		}
		if links >= 16 {
			response.RCode = dnsmessage.RCodeServerFailure
			response.Answers = nil
			return response, nil
		}
		response.Answers = append(response.Answers, aliases...)
		var err error
		name, err = FQDN(aliases[0].Body.(*dnsmessage.CNAMEResource).CNAME.String())
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
