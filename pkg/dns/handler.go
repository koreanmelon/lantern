package dns

import (
	"context"

	"golang.org/x/net/dns/dnsmessage"
)

// Handler processes queries concurrently. Implementations must be safe for
// concurrent calls and return promptly when ctx is canceled.
type Handler interface {
	Handle(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error)
}

// HandlerFunc adapts a function to a Handler.
type HandlerFunc func(context.Context, dnsmessage.Message) (dnsmessage.Message, error)

func (f HandlerFunc) Handle(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
	return f(ctx, query)
}

func reply(query dnsmessage.Message, code dnsmessage.RCode) dnsmessage.Message {
	return dnsmessage.Message{
		Header: dnsmessage.Header{
			ID: query.ID, Response: true, OpCode: query.OpCode,
			RecursionDesired: query.RecursionDesired, RCode: code,
		},
		Questions: query.Questions,
	}
}
