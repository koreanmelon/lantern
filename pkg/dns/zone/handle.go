package zone

import (
	"context"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"golang.org/x/net/dns/dnsmessage"
)

// Handle answers a single IN-class question. ANY, transfers, and OPT questions
// are refused. Responses may share resource bodies with the zone store; callers
// must treat returned records as read-only and must not mutate the query's data.
// DNS outcomes such as NXDOMAIN are normal responses, not Go errors.
// ClientIP is available for application policy but does not affect zone lookup.
func (z *Zone) Handle(ctx context.Context, request dnsserver.Request) (dnsmessage.Message, error) {
	if err := ctx.Err(); err != nil {
		return dnsmessage.Message{}, err
	}
	z.mu.RLock()
	defer z.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return dnsmessage.Message{}, err
	}
	query := request.Message
	name, code := validateQuestion(query)
	response := dnsmessage.Message{Header: dnsmessage.Header{RCode: code}}
	if code == dnsmessage.RCodeSuccess {
		var err error
		response, err = z.lookup(ctx, name, query.Questions[0].Type)
		if err != nil {
			return dnsmessage.Message{}, err
		}
	}
	response.ID = query.ID
	response.Response = true
	response.OpCode = query.OpCode
	response.RecursionDesired = query.RecursionDesired
	response.Questions = query.Questions
	return response, nil
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
	name, err := FQDN(question.Name.String())
	if err != nil {
		return "", dnsmessage.RCodeFormatError
	}
	return name, dnsmessage.RCodeSuccess
}
