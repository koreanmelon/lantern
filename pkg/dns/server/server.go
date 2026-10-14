package dnsserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Server serves DNS over UDP and TCP. Its fields must not change while serving.
type Server struct {
	// Handler processes queries concurrently and must honor context cancellation.
	// A nil handler refuses queries until resolution is configured.
	Handler func(context.Context, dnsmessage.Message) (dnsmessage.Message, error)
	// Timeout bounds each TCP I/O operation and handler invocation. Zero uses 5s.
	Timeout time.Duration
	// MaxConcurrent limits UDP requests and active TCP connections together.
	// Zero uses 128. Requests exceeding the limit are dropped or disconnected.
	MaxConcurrent int
}

type state struct {
	handler func(context.Context, dnsmessage.Message) (dnsmessage.Message, error)
	timeout time.Duration
	slots   chan struct{}
	workers sync.WaitGroup
}

// ListenAndServe binds both transports to address. Port zero selects the same
// ephemeral port for both. The default address is loopback port 1053.
func (s *Server) ListenAndServe(ctx context.Context, address string) error {
	if address == "" {
		address = "127.0.0.1:1053"
	}
	var lc net.ListenConfig
	tcp, err := lc.Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("listen DNS TCP: %w", err)
	}
	defer tcp.Close()
	udp, err := lc.ListenPacket(ctx, "udp", tcp.Addr().String())
	if err != nil {
		return fmt.Errorf("listen DNS UDP: %w", err)
	}
	defer udp.Close()
	return s.Serve(ctx, udp, tcp)
}

// Serve owns and closes both listeners, including on cancellation or failure.
// It waits for active handlers to finish; handlers must honor cancellation.
func (s *Server) Serve(ctx context.Context, udp net.PacketConn, tcp net.Listener) error {
	if udp != nil {
		defer udp.Close()
	}
	if tcp != nil {
		defer tcp.Close()
	}
	if udp == nil || tcp == nil {
		return errors.New("DNS requires both UDP and TCP listeners")
	}
	if s.Timeout < 0 || s.MaxConcurrent < 0 {
		return errors.New("DNS timeout and concurrency limit must not be negative")
	}
	timeout, limit := s.Timeout, s.MaxConcurrent
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if limit == 0 {
		limit = 128
	}
	st := state{handler: s.Handler, timeout: timeout, slots: make(chan struct{}, limit)}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(serveCtx, func() {
		udp.Close()
		tcp.Close()
	})
	defer stop()
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- st.serveUDP(serveCtx, udp) }()
	go func() { errorsCh <- st.serveTCP(serveCtx, tcp) }()
	err := <-errorsCh
	cancel()
	// Close synchronously as well, so both loops stop before waiting on workers.
	udp.Close()
	tcp.Close()
	<-errorsCh
	st.workers.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
