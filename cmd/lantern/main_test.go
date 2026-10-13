package main

import (
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestDummyResponse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		typeCode    dnsmessage.Type
		class       dnsmessage.Class
		wantCode    dnsmessage.RCode
		wantAnswers int
	}{
		{"A", dnsmessage.TypeA, dnsmessage.ClassINET, dnsmessage.RCodeSuccess, 1},
		{"AAAA", dnsmessage.TypeAAAA, dnsmessage.ClassINET, dnsmessage.RCodeRefused, 0},
		{"MX", dnsmessage.TypeMX, dnsmessage.ClassINET, dnsmessage.RCodeRefused, 0},
		{"non-INET", dnsmessage.TypeA, dnsmessage.ClassCHAOS, dnsmessage.RCodeRefused, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := dnsmessage.Message{
				Header: dnsmessage.Header{ID: 123, RecursionDesired: true},
				Questions: []dnsmessage.Question{{
					Name: dnsmessage.MustNewName("printer.home.arpa."),
					Type: tc.typeCode, Class: tc.class,
				}},
			}
			response, err := dummyResponse(t.Context(), query)
			if err != nil {
				t.Fatal(err)
			}
			packet, err := response.Pack()
			if err != nil {
				t.Fatal(err)
			}
			var got dnsmessage.Message
			if err := got.Unpack(packet); err != nil {
				t.Fatal(err)
			}
			if got.ID != query.ID || !got.Response || !got.RecursionDesired || got.RecursionAvailable ||
				got.RCode != tc.wantCode || len(got.Answers) != tc.wantAnswers ||
				len(got.Questions) != 1 || got.Questions[0] != query.Questions[0] {
				t.Fatalf("unexpected response: %s", got.GoString())
			}
			if tc.wantAnswers == 1 {
				answer := got.Answers[0]
				body, ok := answer.Body.(*dnsmessage.AResource)
				if answer.Header.Name != query.Questions[0].Name || answer.Header.Class != dnsmessage.ClassINET ||
					answer.Header.Type != dnsmessage.TypeA || answer.Header.TTL != 60 ||
					!ok || body.A != [4]byte{192, 168, 1, 20} {
					t.Fatalf("unexpected A record: %s", answer.GoString())
				}
			}
		})
	}
}
