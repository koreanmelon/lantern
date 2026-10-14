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
	// A nil handler returns REFUSED.
	Handler func(context.Context, dnsmessage.Message) (dnsmessage.Message, error)
	// Timeout bounds TCP I/O and handler calls. Zero uses DefaultTimeout.
	Timeout time.Duration
	// Concurrency limits UDP requests and TCP connections together.
	// Zero uses DefaultConcurrency.
	Concurrency int
}

func (c *Config) setDefaults() error {
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
