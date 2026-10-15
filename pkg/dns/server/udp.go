package dnsserver

import (
	"context"
	"fmt"
	"net"
	"time"
)

func (s *Server) serveUDP(ctx context.Context, conn net.PacketConn) error {
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

			response, err := s.response(ctx, packet, clientIP(address), true)
			if err == nil && ctx.Err() == nil {
				// Per-request send errors do not stop the listener.
				_ = s.writeUDP(ctx, conn, response, address)
			}
		})
	}
}

func (s *Server) writeUDP(ctx context.Context, conn net.PacketConn, packet []byte, address net.Addr) error {
	s.udpWrites.Lock()
	defer s.udpWrites.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(s.Config.WriteTimeout)); err != nil {
		return err
	}
	_, err := conn.WriteTo(packet, address)
	return err
}
