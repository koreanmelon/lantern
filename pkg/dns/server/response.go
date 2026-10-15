package dnsserver

import (
	"context"
	"errors"
	"net/netip"

	"golang.org/x/net/dns/dnsmessage"
)

func (s *Server) response(ctx context.Context, packet []byte, ip netip.Addr, udp bool) ([]byte, error) {
	var query dnsmessage.Message
	if err := query.Unpack(packet); err != nil {
		return nil, err
	}

	if query.Response {
		return nil, errors.New("received a DNS response, not a query")
	}

	var response dnsmessage.Message
	switch {
	case query.OpCode != 0:
		response.RCode = dnsmessage.RCodeNotImplemented
	case len(query.Questions) != 1:
		response.RCode = dnsmessage.RCodeFormatError
	case s.Config.Handler == nil:
		response.RCode = dnsmessage.RCodeRefused
	default:
		handlerCtx, cancel := context.WithTimeout(ctx, s.Config.HandlerTimeout)
		defer cancel()

		var err error
		response, err = s.Config.Handler(handlerCtx, Request{Message: query, ClientIP: ip})
		if err != nil {
			response = dnsmessage.Message{RCode: dnsmessage.RCodeServerFailure}
		}
	}

	response.ID = query.ID
	response.Response = true
	response.OpCode = query.OpCode
	response.RecursionDesired = query.RecursionDesired
	response.Questions = query.Questions

	encoded, err := response.Pack()
	if err != nil {
		return nil, err
	}

	// EDNS is not supported yet. Always respect the classic 512-byte UDP limit.
	if udp && len(encoded) > 512 {
		response.Truncated = true
		response.Answers = nil
		response.Authorities = nil
		response.Additionals = nil
		encoded, err = response.Pack()
		if len(encoded) > 512 {
			response.Questions = nil
			encoded, err = response.Pack()
		}
	}
	return encoded, err
}
