package dns

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
	// A nil Handler refuses queries until resolution is configured.
	Handler Handler
	// Timeout bounds each TCP I/O operation and handler invocation. Zero uses 5s.
	Timeout time.Duration
	// MaxConcurrent limits UDP requests and active TCP connections together.
	// Zero uses 128. Requests exceeding the limit are dropped or disconnected.
	MaxConcurrent int
}

type runtime struct {
	server  *Server
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
	r := runtime{server: s, timeout: timeout, slots: make(chan struct{}, limit)}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(serveCtx, func() {
		udp.Close()
		tcp.Close()
	})
	defer stop()
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- r.serveUDP(serveCtx, udp) }()
	go func() { errorsCh <- r.serveTCP(serveCtx, tcp) }()
	err := <-errorsCh
	cancel()
	// Close synchronously as well, so both loops stop before waiting on workers.
	udp.Close()
	tcp.Close()
	<-errorsCh
	r.workers.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (r *runtime) response(ctx context.Context, packet []byte, udp bool) ([]byte, error) {
	query, err := Parse(packet)
	if err != nil {
		return nil, err
	}
	if query.Response {
		return nil, errors.New("received a DNS response, not a query")
	}
	var response dnsmessage.Message
	switch {
	case query.OpCode != 0:
		response = reply(query, dnsmessage.RCodeNotImplemented)
	case len(query.Questions) != 1:
		response = reply(query, dnsmessage.RCodeFormatError)
	case r.server.Handler == nil:
		response = reply(query, dnsmessage.RCodeRefused)
	default:
		handlerCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		response, err = r.server.Handler.Handle(handlerCtx, query)
		if err != nil {
			response = reply(query, dnsmessage.RCodeServerFailure)
		}
	}
	response.ID = query.ID
	response.Response = true
	response.OpCode = query.OpCode
	response.RecursionDesired = query.RecursionDesired
	response.Questions = query.Questions
	encoded, err := response.Pack()
	if err != nil {
		return nil, err
	}
	// EDNS is not supported yet. Always respect the classic 512-byte UDP limit.
	if udp && len(encoded) > 512 {
		response.Truncated = true
		response.Answers = nil
		response.Authorities = nil
		response.Additionals = nil
		encoded, err = response.Pack()
		if len(encoded) > 512 {
			response.Questions = nil
			encoded, err = response.Pack()
		}
	}
	return encoded, err
}
