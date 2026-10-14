package dnsserver

import (
	"context"
	"errors"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	DefaultAddress     string        = "127.0.0.1:1053"
	DefaultTimeout     time.Duration = time.Second
	DefaultConcurrency int           = 100
)

type Config struct {
	// Address is the UDP/TCP listen address used by ListenAndServe.
	// Serve uses the supplied listeners instead.
	Address string
	// Handler must be safe for concurrent calls and honor context cancellation.
	// It receives one standard query question and owns the returned RCode,
	// Authoritative/RecursionAvailable flags, and resource-record sections.
	// The server sets ID, Response, OpCode, RecursionDesired, and Questions from
	// the request, and handles framing and UDP truncation. Other returned flags
	// are preserved. Handlers must not mutate the query's Questions slice or
	// modify returned data after returning it.
	// DNS outcomes such as NXDOMAIN are responses, not errors. A Go error
	// discards the returned message and produces SERVFAIL. A nil handler
	// returns REFUSED.
	Handler func(context.Context, dnsmessage.Message) (dnsmessage.Message, error)
	// Timeout bounds TCP I/O and handler calls. Zero uses DefaultTimeout.
	Timeout time.Duration
	// Concurrency limits UDP requests and TCP connections together.
	// Zero uses DefaultConcurrency.
	Concurrency int
}

func (c *Config) SetDefaults() error {
	if c.Timeout < 0 || c.Concurrency < 0 {
		return errors.New("DNS timeout and concurrency limit must not be negative")
	}
	if c.Address == "" {
		c.Address = DefaultAddress
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Concurrency == 0 {
		c.Concurrency = DefaultConcurrency
	}
	return nil
}
