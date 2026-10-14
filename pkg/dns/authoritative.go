package dns

import (
	"context"
	"sync/atomic"

	"golang.org/x/net/dns/dnsmessage"
)

// Authoritative answers only from its configured zones, without recursion or
// forwarding. It is safe for concurrent queries and zone replacement. Its zero
// value refuses all queries. It must not be copied after first use.
type Authoritative struct {
	snapshot atomic.Pointer[zoneSnapshot]
}

// NewAuthoritative validates and installs an initial set of immutable zones.
func NewAuthoritative(zones ...*Zone) (*Authoritative, error) {
	a := &Authoritative{}
	if err := a.ReplaceZones(zones...); err != nil {
		return nil, err
	}
	return a, nil
}

// ReplaceZones replaces the complete zone set, not just matching origins.
// Validation failure leaves the current set untouched. Each query uses one
// snapshot throughout its lookup, including cross-zone CNAME chains.
// With no arguments it removes all zones. Concurrent replacements are safe;
// the last publication wins. Callers composing edits must coordinate them.
func (a *Authoritative) ReplaceZones(zones ...*Zone) error {
	snapshot, err := newZoneSnapshot(zones)
	if err != nil {
		return err
	}
	a.snapshot.Store(snapshot)
	return nil
}

// Handle answers a single IN-class question. ANY, transfers, and OPT questions
// are refused. Responses do not share mutable record data with the zone store.
// DNS outcomes such as NXDOMAIN are normal responses, not Go errors.
func (a *Authoritative) Handle(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
	if err := ctx.Err(); err != nil {
		return dnsmessage.Message{}, err
	}
	name, code := validateQuestion(query)
	response := dnsmessage.Message{Header: dnsmessage.Header{RCode: code}}
	if code == dnsmessage.RCodeSuccess {
		// Load exactly once so an update cannot mix zones within a query.
		snapshot := a.snapshot.Load()
		var err error
		response, err = snapshot.lookup(ctx, name, query.Questions[0].Type)
		if err != nil {
			return dnsmessage.Message{}, err
		}
	}
	response.ID = query.ID
	response.Response = true
	response.OpCode = query.OpCode
	response.RecursionDesired = query.RecursionDesired
	response.Questions = query.Questions
	if code != dnsmessage.RCodeSuccess {
		return response, nil
	}
	return copyMessage(response)
}

func validateQuestion(query dnsmessage.Message) (string, dnsmessage.RCode) {
	if query.Response || len(query.Questions) != 1 {
		return "", dnsmessage.RCodeFormatError
	}
	if query.OpCode != 0 {
		return "", dnsmessage.RCodeNotImplemented
	}
	question := query.Questions[0]
	switch question.Type {
	case dnsmessage.TypeALL, dnsmessage.TypeOPT, 251, 252, 253, 254:
		// ANY, OPT, IXFR, AXFR, MAILB, and MAILA are not ordinary lookups.
		return "", dnsmessage.RCodeRefused
	}
	if question.Class != dnsmessage.ClassINET {
		return "", dnsmessage.RCodeRefused
	}
	name, err := canonicalName(question.Name.String())
	if err != nil {
		return "", dnsmessage.RCodeFormatError
	}
	return name, dnsmessage.RCodeSuccess
}
