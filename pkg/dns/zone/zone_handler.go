package zone

import (
	"context"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

// ZoneHandler answers only from its configured zones, without recursion or
// forwarding. It is safe for concurrent queries and zone replacement. Its zero
// value refuses all queries. It must not be copied after first use.
type ZoneHandler struct {
	mu    sync.RWMutex
	zones []*Zone
}

// NewZoneHandler validates and installs an initial set of zones.
func NewZoneHandler(zones ...*Zone) (*ZoneHandler, error) {
	h := &ZoneHandler{}
	if err := h.ReplaceZones(zones...); err != nil {
		return nil, err
	}
	return h, nil
}

// ReplaceZones replaces the complete zone set, not just matching origins.
// Validation failure leaves the current set untouched. Queries hold a read lock
// throughout lookup and response copying, including cross-zone CNAME chains.
// With no arguments it removes all zones. Concurrent replacements are safe;
// the last assignment wins. Callers composing edits must coordinate them.
func (h *ZoneHandler) ReplaceZones(zones ...*Zone) error {
	replacement, err := prepareZones(zones)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.zones = replacement
	h.mu.Unlock()
	return nil
}

// Handle answers a single IN-class question. ANY, transfers, and OPT questions
// are refused. Responses do not share mutable record data with the zone store.
// DNS outcomes such as NXDOMAIN are normal responses, not Go errors.
func (h *ZoneHandler) Handle(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
	if err := ctx.Err(); err != nil {
		return dnsmessage.Message{}, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	name, code := validateQuestion(query)
	response := dnsmessage.Message{Header: dnsmessage.Header{RCode: code}}
	if code == dnsmessage.RCodeSuccess {
		var err error
		response, err = lookup(ctx, h.zones, name, query.Questions[0].Type)
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
