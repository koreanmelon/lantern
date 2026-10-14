package dnsserver

import (
	"context"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestConfigDefaults(t *testing.T) {
	for _, tc := range []struct {
		name        string
		config      Config
		address     string
		timeout     time.Duration
		concurrency int
	}{
		{"zero values", Config{}, DefaultAddress, DefaultTimeout, DefaultConcurrency},
		{"explicit values", Config{Address: "127.0.0.1:2053", Timeout: 10 * time.Second, Concurrency: 10}, "127.0.0.1:2053", 10 * time.Second, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config
			config.Handler = func(context.Context, dnsmessage.Message) (dnsmessage.Message, error) {
				return dnsmessage.Message{Header: dnsmessage.Header{RCode: dnsmessage.RCodeRefused}}, nil
			}
			// Applying defaults repeatedly must not overwrite explicit values.
			for range 2 {
				if err := config.setDefaults(); err != nil {
					t.Fatal(err)
				}
			}
			if config.Address != tc.address || config.Timeout != tc.timeout || config.Concurrency != tc.concurrency {
				t.Fatalf("incorrect defaults: %+v", config)
			}
			response, err := config.Handler(t.Context(), dnsmessage.Message{})
			if err != nil || response.RCode != dnsmessage.RCodeRefused {
				t.Fatal("defaults replaced the handler")
			}
		})
	}
}

func TestConfigRejectsNegativeValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
	}{
		{"timeout", Config{Timeout: -time.Second}},
		{"concurrency", Config{Concurrency: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.setDefaults(); err == nil {
				t.Fatal("accepted negative value")
			}
		})
	}
}
