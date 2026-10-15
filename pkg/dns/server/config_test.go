package dnsserver

import (
	"context"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestConfigDefaults(t *testing.T) {
	for _, tc := range []struct {
		name           string
		config         Config
		address        string
		readTimeout    time.Duration
		writeTimeout   time.Duration
		handlerTimeout time.Duration
		concurrency    int
	}{
		{"zero values", Config{}, DefaultAddress, DefaultReadTimeout, DefaultWriteTimeout, DefaultHandlerTimeout, DefaultConcurrency},
		{"explicit values", Config{Address: "127.0.0.1:2053", ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, HandlerTimeout: 4 * time.Second, Concurrency: 10}, "127.0.0.1:2053", 2 * time.Second, 3 * time.Second, 4 * time.Second, 10},
		{"partial values", Config{WriteTimeout: 5 * time.Second}, DefaultAddress, DefaultReadTimeout, 5 * time.Second, DefaultHandlerTimeout, DefaultConcurrency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config
			config.Handler = func(context.Context, Request) (dnsmessage.Message, error) {
				return dnsmessage.Message{Header: dnsmessage.Header{RCode: dnsmessage.RCodeRefused}}, nil
			}
			// Applying defaults repeatedly must not overwrite explicit values.
			for range 2 {
				if err := config.Normalize(); err != nil {
					t.Fatal(err)
				}
			}
			if config.Address != tc.address || config.ReadTimeout != tc.readTimeout || config.WriteTimeout != tc.writeTimeout || config.HandlerTimeout != tc.handlerTimeout || config.Concurrency != tc.concurrency {
				t.Fatalf("incorrect defaults: %+v", config)
			}
			response, err := config.Handler(t.Context(), Request{})
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
		{"read timeout", Config{ReadTimeout: -time.Second}},
		{"write timeout", Config{WriteTimeout: -time.Second}},
		{"handler timeout", Config{HandlerTimeout: -time.Second}},
		{"concurrency", Config{Concurrency: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Normalize(); err == nil {
				t.Fatal("accepted negative value")
			}
		})
	}
}
