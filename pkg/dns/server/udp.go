package dnsserver

import (
	"context"
	"fmt"
	"net"
)

func (s *state) serveUDP(ctx context.Context, conn net.PacketConn) error {
	buffer := make([]byte, 65535)
	for {
		n, address, err := conn.ReadFrom(buffer)
		if err != nil {
			return fmt.Errorf("read DNS UDP: %w", err)
		}
		select {
		case s.slots <- struct{}{}:
		default:
			continue
		}
		packet := append([]byte(nil), buffer[:n]...)
		s.workers.Go(func() {
			defer func() { <-s.slots }()
			response, err := s.response(ctx, packet, true)
			if err == nil && ctx.Err() == nil {
				// Per-request send errors do not stop the listener.
				_, _ = conn.WriteTo(response, address)
			}
		})
	}
}
