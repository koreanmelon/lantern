package dnsserver

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestHandlerResponseContract(t *testing.T) {
	query := testQuery(t)
	question := query.Questions[0]
	handled := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID: 999, OpCode: 2, Authoritative: true, RecursionAvailable: true,
			AuthenticData: true, CheckingDisabled: true, RCode: dnsmessage.RCodeNameError,
		},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("wrong.example."), Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET}},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("missing.example.com.")},
		}},
		Authorities: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns.example.com."), MBox: dnsmessage.MustNewName("hostmaster.example.com."), MinTTL: 60},
		}},
		Additionals: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("ns.example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
		}},
	}
	for _, tc := range []struct {
		name         string
		handlerError error
	}{
		{"DNS negative answer", nil}, {"Go error", errors.New("handler failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			s := Server{Config: Config{Timeout: time.Second, Handler: func(ctx context.Context, got dnsmessage.Message) (dnsmessage.Message, error) {
				called = true
				if got.ID != query.ID || !reflect.DeepEqual(got.Questions, query.Questions) {
					t.Fatal("handler received the wrong query")
				}
				return handled, tc.handlerError
			}}}
			packet, err := query.Pack()
			if err != nil {
				t.Fatal(err)
			}
			packet, err = s.response(t.Context(), packet, false)
			if err != nil {
				t.Fatal(err)
			}
			var got dnsmessage.Message
			if err := got.Unpack(packet); err != nil {
				t.Fatal(err)
			}
			if !called || got.ID != query.ID || !got.Response || got.OpCode != query.OpCode ||
				got.RecursionDesired != query.RecursionDesired || !reflect.DeepEqual(got.Questions, query.Questions) {
				t.Fatalf("transport did not restore the request envelope: %s", got.GoString())
			}
			if tc.handlerError != nil {
				if got.RCode != dnsmessage.RCodeServerFailure || got.Authoritative || got.RecursionAvailable || got.AuthenticData || got.CheckingDisabled ||
					len(got.Answers)+len(got.Authorities)+len(got.Additionals) != 0 {
					t.Fatal("handler error did not discard returned data")
				}
				return
			}
			if got.RCode != handled.RCode || !got.Authoritative || !got.RecursionAvailable || !got.AuthenticData || !got.CheckingDisabled ||
				len(got.Answers) != 1 || len(got.Authorities) != 1 || len(got.Additionals) != 1 {
				t.Fatalf("transport did not preserve handler-owned data: %s", got.GoString())
			}
		})
	}
}

func TestHandlerContext(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "request value"))
	defer cancel()
	s := Server{Config: Config{Timeout: time.Second, Handler: func(handlerCtx context.Context, _ dnsmessage.Message) (dnsmessage.Message, error) {
		if handlerCtx.Value(contextKey{}) != "request value" {
			t.Fatal("request context was not propagated")
		}
		deadline, ok := handlerCtx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Fatal("handler timeout was not applied")
		}
		cancel()
		if handlerCtx.Err() != context.Canceled {
			t.Fatal("parent cancellation was not propagated")
		}
		return dnsmessage.Message{}, handlerCtx.Err()
	}}}
	query := testQuery(t)
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.response(ctx, packet, true); err != nil {
		t.Fatal(err)
	}
}
