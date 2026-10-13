package dns

import (
	"context"
	"fmt"
	"net"
)

func (r *runtime) serveUDP(ctx context.Context, conn net.PacketConn) error {
	buffer := make([]byte, 65535)
	for {
		n, address, err := conn.ReadFrom(buffer)
		if err != nil {
			return fmt.Errorf("read DNS UDP: %w", err)
		}
		select {
		case r.slots <- struct{}{}:
		default:
			continue
		}
		packet := append([]byte(nil), buffer[:n]...)
		r.workers.Go(func() {
			defer func() { <-r.slots }()
			response, err := r.response(ctx, packet, true)
			if err == nil && ctx.Err() == nil {
				// Per-request send errors do not stop the listener.
				_, _ = conn.WriteTo(response, address)
			}
		})
	}
}
