package dnsserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// ErrAlreadyServing indicates that a Server already has an active Serve call.
var ErrAlreadyServing = errors.New("DNS server is already serving")

// Server serves DNS over UDP and TCP. Set Config before serving; startup fills
// its zero-valued settings with defaults. Do not modify Config while serving.
// A Server must not be copied after first use.
type Server struct {
	Config Config

	serving atomic.Bool
	slots   chan struct{}
	workers sync.WaitGroup
}

// ListenAndServe binds both transports to Config.Address. Port zero selects the
// same ephemeral port for both. It rejects overlapping serving calls.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if !s.serving.CompareAndSwap(false, true) {
		return ErrAlreadyServing
	}
	defer s.serving.Store(false)
	if err := s.Config.setDefaults(); err != nil {
		return err
	}
	var lc net.ListenConfig
	tcp, err := lc.Listen(ctx, "tcp", s.Config.Address)
	if err != nil {
		return fmt.Errorf("listen DNS TCP: %w", err)
	}
	defer tcp.Close()
	udp, err := lc.ListenPacket(ctx, "udp", tcp.Addr().String())
	if err != nil {
		return fmt.Errorf("listen DNS UDP: %w", err)
	}
	defer udp.Close()
	return s.serve(ctx, udp, tcp)
}

// Serve owns and closes both listeners, including on cancellation or failure.
// An overlapping call returns ErrAlreadyServing without taking ownership of
// its listeners. Sequential calls are allowed after Serve returns.
// Serve waits for active handlers to finish; handlers must honor cancellation.
func (s *Server) Serve(ctx context.Context, udp net.PacketConn, tcp net.Listener) error {
	if !s.serving.CompareAndSwap(false, true) {
		return ErrAlreadyServing
	}
	defer s.serving.Store(false)
	if udp != nil {
		defer udp.Close()
	}
	if tcp != nil {
		defer tcp.Close()
	}
	if udp == nil || tcp == nil {
		return errors.New("DNS requires both UDP and TCP listeners")
	}
	if err := s.Config.setDefaults(); err != nil {
		return err
	}
	return s.serve(ctx, udp, tcp)
}

func (s *Server) serve(ctx context.Context, udp net.PacketConn, tcp net.Listener) error {
	s.slots = make(chan struct{}, s.Config.Concurrency)
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(serveCtx, func() {
		udp.Close()
		tcp.Close()
	})
	defer stop()
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- s.serveUDP(serveCtx, udp) }()
	go func() { errorsCh <- s.serveTCP(serveCtx, tcp) }()
	err := <-errorsCh
	cancel()
	// Close synchronously as well, so both loops stop before waiting on workers.
	udp.Close()
	tcp.Close()
	<-errorsCh
	s.workers.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
