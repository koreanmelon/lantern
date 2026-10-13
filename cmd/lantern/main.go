package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns"
	"golang.org/x/net/dns/dnsmessage"
)

func main() {
	address := flag.String("dns-listen", "127.0.0.1:1053", "DNS UDP and TCP listen address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := dns.Server{
		Handler:       dns.HandlerFunc(dummyResponse),
		Timeout:       10 * time.Second,
		MaxConcurrent: 10,
	}

	log.Printf("Starting DNS server on %s", *address)
	if err := server.ListenAndServe(ctx, *address); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// dummyResponse is a test handler, not a resolver: every A/INET query receives
// the same address, regardless of its name.
func dummyResponse(_ context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
	response := dnsmessage.Message{
		ID: query.ID, Response: true, OpCode: query.OpCode,
		RecursionDesired: query.RecursionDesired,
		Questions:        query.Questions,
	}
	if len(query.Questions) != 1 {
		response.RCode = dnsmessage.RCodeFormatError
		return response, nil
	}
	question := query.Questions[0]
	if question.Type != dnsmessage.TypeA || question.Class != dnsmessage.ClassINET {
		response.RCode = dnsmessage.RCodeRefused
		return response, nil
	}
	response.Answers = []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{
			Name: question.Name, Type: dnsmessage.TypeA,
			Class: dnsmessage.ClassINET, TTL: 60,
		},
		Body: &dnsmessage.AResource{A: [4]byte{192, 168, 1, 20}},
	}}
	return response, nil
}
