package dnsserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Server serves DNS over UDP and TCP. Set Config before serving; startup fills
// its zero-valued settings with defaults. Do not modify Config while serving.
// A Server must not be copied after first use.
type Server struct {
	Config Config

	serving   atomic.Bool
	slots     chan struct{}
	workers   sync.WaitGroup
	udpWrites sync.Mutex
}

// ListenAndServe binds both transports to Config.Address. Port zero selects the
// same ephemeral port for both. It rejects overlapping serving calls.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if !s.serving.CompareAndSwap(false, true) {
		return errors.New("server is already serving")
	}
	defer s.serving.Store(false)

	if err := s.Config.Normalize(); err != nil {
		return err
	}

	var lc net.ListenConfig

	// UDP
	udp, err := lc.ListenPacket(ctx, "udp", s.Config.Address)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP socket: %w", err)
	}
	defer udp.Close()

	// TCP
	tcp, err := lc.Listen(ctx, "tcp", udp.LocalAddr().String())
	if err != nil {
		return fmt.Errorf("failed to listen on TCP socket: %w", err)
	}
	defer tcp.Close()

	return s.serve(ctx, udp, tcp)
}

// Serve owns and closes both listeners, including on cancellation or failure.
// An overlapping call returns an error without taking ownership of
// its listeners. Sequential calls are allowed after Serve returns.
// Serve waits for active handlers to finish; handlers must honor cancellation.
func (s *Server) Serve(ctx context.Context, udp net.PacketConn, tcp net.Listener) error {
	if !s.serving.CompareAndSwap(false, true) {
		return errors.New("server is already serving")
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

	if err := s.Config.Normalize(); err != nil {
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

	errors := make(chan error, 2)
	go func() { errors <- s.serveUDP(serveCtx, udp) }()
	go func() { errors <- s.serveTCP(serveCtx, tcp) }()
	err := <-errors
	cancel()

	udp.Close()
	tcp.Close()
	<-errors
	s.workers.Wait()
	if ctx.Err() != nil {
		return nil
	}

	return err
}
