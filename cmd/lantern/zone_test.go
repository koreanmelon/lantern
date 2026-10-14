package main

import (
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestExampleZone(t *testing.T) {
	handler, err := exampleHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		code    dnsmessage.RCode
		answers int
	}{
		{"lantern.home.arpa.", dnsmessage.RCodeSuccess, 1},
		{"pi.home.arpa.", dnsmessage.RCodeSuccess, 2},
		{"missing.home.arpa.", dnsmessage.RCodeNameError, 0},
		{"example.com.", dnsmessage.RCodeRefused, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := dnsmessage.Message{
				Header:    dnsmessage.Header{ID: 123, RecursionDesired: true},
				Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(tc.name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
			}
			response, err := handler.Handle(t.Context(), query)
			if err != nil {
				t.Fatal(err)
			}
			if response.ID != query.ID || response.RCode != tc.code || len(response.Answers) != tc.answers {
				t.Fatalf("unexpected response: %s", response.GoString())
			}
			if tc.answers > 0 {
				body := response.Answers[tc.answers-1].Body.(*dnsmessage.AResource)
				if body.A != [4]byte{192, 168, 1, 2} {
					t.Fatal("incorrect Pi address")
				}
			}
		})
	}
}
