package dnsserver

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
		handler    func(context.Context, dnsmessage.Message) (dnsmessage.Message, error)
		code       dnsmessage.RCode
	}{
		{name: "default refused", code: dnsmessage.RCodeRefused},
		{name: "unsupported opcode", opcode: 2, code: dnsmessage.RCodeNotImplemented},
		{name: "missing question", noQuestion: true, code: dnsmessage.RCodeFormatError},
		{name: "handler error", handler: func(context.Context, dnsmessage.Message) (dnsmessage.Message, error) {
			return dnsmessage.Message{}, errors.New("failed")
		}, code: dnsmessage.RCodeServerFailure},
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
			r := state{handler: tc.handler, timeout: time.Second}
			encoded, err := r.response(t.Context(), packet, true)
			if err != nil {
				t.Fatal(err)
			}
			var got dnsmessage.Message
			if err := got.Unpack(encoded); err != nil {
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
	r := state{timeout: time.Second, handler: func(context.Context, dnsmessage.Message) (dnsmessage.Message, error) {
		var response dnsmessage.Message
		for range 100 {
			response.Answers = append(response.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Class: dnsmessage.ClassINET, TTL: 60},
				Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
			})
		}
		return response, nil
	},
	}
	for _, udp := range []bool{true, false} {
		encoded, err := r.response(t.Context(), packet, udp)
		if err != nil {
			t.Fatal(err)
		}
		var message dnsmessage.Message
		if err := message.Unpack(encoded); err != nil {
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
	r := state{timeout: time.Second}
	for _, tc := range []struct {
		name   string
		packet []byte
	}{
		{"empty", nil},
		{"short header", make([]byte, 11)},
		{"missing question", []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}},
		{"pointer loop", []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0x0c, 0, 1, 0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.response(t.Context(), tc.packet, true); err == nil {
				t.Fatal("accepted malformed packet")
			}
		})
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

func FuzzResponse(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 12))
	f.Add([]byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0x0c, 0, 1, 0, 1})
	query := dnsmessage.Message{
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	packet, err := query.Pack()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(packet)
	f.Fuzz(func(t *testing.T, packet []byte) {
		s := state{timeout: time.Second}
		response, err := s.response(t.Context(), packet, true)
		if err != nil {
			return
		}
		var message dnsmessage.Message
		if err := message.Unpack(response); err != nil {
			t.Fatalf("invalid response: %v", err)
		}
		if !message.Response || len(response) > 512 {
			t.Fatal("invalid UDP response envelope")
		}
	})
}
