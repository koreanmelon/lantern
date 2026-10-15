package dnsserver

import (
	"context"
	"errors"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	DefaultAddress        string        = "127.0.0.1:1053"
	DefaultReadTimeout    time.Duration = time.Second
	DefaultWriteTimeout   time.Duration = time.Second
	DefaultHandlerTimeout time.Duration = time.Second
	DefaultConcurrency    int           = 100
)

type Config struct {
	// Address is the UDP/TCP listen address used by ListenAndServe.
	// Serve uses the supplied listeners instead.
	Address string
	// Handler must be safe for concurrent calls and honor context cancellation.
	// Request.Message contains a single-question standard query. The handler
	// owns the returned RCode,
	// Authoritative/RecursionAvailable flags, and resource-record sections.
	// The server sets ID, Response, OpCode, RecursionDesired, and Questions from
	// the request, and handles framing and UDP truncation. Other returned flags
	// are preserved. Handlers must not mutate the query's Questions slice or
	// modify returned data after returning it.
	// DNS outcomes such as NXDOMAIN are responses, not errors. A Go error
	// discards the returned message and produces SERVFAIL. A nil handler
	// returns REFUSED.
	Handler func(context.Context, Request) (dnsmessage.Message, error)
	// ReadTimeout bounds reading one complete TCP frame, including its prefix.
	// It does not time out an idle UDP listener. Zero uses DefaultReadTimeout.
	ReadTimeout time.Duration
	// WriteTimeout bounds each TCP write or UDP send. UDP sends are serialized
	// because write deadlines apply to the entire socket; the timeout starts
	// after acquiring the send lock. Zero uses DefaultWriteTimeout.
	WriteTimeout time.Duration
	// HandlerTimeout is the handler's cooperative deadline, not a hard execution
	// limit or a total request budget. Zero uses DefaultHandlerTimeout.
	HandlerTimeout time.Duration
	// Concurrency limits UDP requests and TCP connections together.
	// Zero uses DefaultConcurrency.
	Concurrency int
}

// Normalize validates settings and fills zero values with defaults. It mutates
// Config and must not run concurrently with serving. Startup calls it automatically.
func (c *Config) Normalize() error {
	if c.ReadTimeout < 0 || c.WriteTimeout < 0 || c.HandlerTimeout < 0 || c.Concurrency < 0 {
		return errors.New("DNS timeout and concurrency limit must not be negative")
	}
	if c.Address == "" {
		c.Address = DefaultAddress
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = DefaultReadTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}
	if c.HandlerTimeout == 0 {
		c.HandlerTimeout = DefaultHandlerTimeout
	}
	if c.Concurrency == 0 {
		c.Concurrency = DefaultConcurrency
	}
	return nil
}
