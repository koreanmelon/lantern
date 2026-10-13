package dns

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func testQuery(t *testing.T) dnsmessage.Message {
	t.Helper()
	return dnsmessage.Message{
		Header: dnsmessage.Header{ID: 42, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name: dnsmessage.MustNewName("example.com."),
			Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
		}},
	}
}

func TestResponseCodes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opcode     dnsmessage.OpCode
		noQuestion bool
		handler    Handler
		code       dnsmessage.RCode
	}{
		{name: "default refused", code: dnsmessage.RCodeRefused},
		{name: "unsupported opcode", opcode: 2, code: dnsmessage.RCodeNotImplemented},
		{name: "missing question", noQuestion: true, code: dnsmessage.RCodeFormatError},
		{name: "handler error", handler: HandlerFunc(func(context.Context, dnsmessage.Message) (dnsmessage.Message, error) {
			return dnsmessage.Message{}, errors.New("failed")
		}), code: dnsmessage.RCodeServerFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := testQuery(t)
			query.OpCode = tc.opcode
			if tc.noQuestion {
				query.Questions = nil
			}
			packet, err := query.Pack()
			if err != nil {
				t.Fatal(err)
			}
			r := state{server: &Server{Handler: tc.handler}, timeout: time.Second}
			encoded, err := r.response(t.Context(), packet, true)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Parse(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if got.RCode != tc.code || got.ID != query.ID || !got.Response ||
				!got.RecursionDesired || got.OpCode != query.OpCode {
				t.Fatalf("unexpected response: %s", got.GoString())
			}
		})
	}
}

func TestResponseTruncation(t *testing.T) {
	query := testQuery(t)
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	r := state{timeout: time.Second, server: &Server{Handler: HandlerFunc(
		func(context.Context, dnsmessage.Message) (dnsmessage.Message, error) {
			response := reply(query, dnsmessage.RCodeSuccess)
			for range 100 {
				response.Answers = append(response.Answers, dnsmessage.Resource{
					Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
				})
			}
			return response, nil
		}),
	}}
	for _, udp := range []bool{true, false} {
		encoded, err := r.response(t.Context(), packet, udp)
		if err != nil {
			t.Fatal(err)
		}
		message, err := Parse(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if udp {
			if len(encoded) > 512 || !message.Truncated || len(message.Answers) != 0 {
				t.Fatal("UDP response was not safely truncated")
			}
		} else if message.Truncated || len(message.Answers) != 100 {
			t.Fatal("TCP response lost answers")
		}
	}
}

func TestResponseRejectsInvalidInput(t *testing.T) {
	r := state{server: &Server{}, timeout: time.Second}
	if _, err := r.response(t.Context(), []byte{1}, true); err == nil {
		t.Fatal("accepted malformed packet")
	}
	query := testQuery(t)
	query.Response = true
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.response(t.Context(), packet, true); err == nil {
		t.Fatal("answered a response packet")
	}
}
