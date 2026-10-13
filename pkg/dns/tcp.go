package dns

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

func (s *state) serveTCP(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept DNS TCP: %w", err)
		}
		select {
		case s.slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		s.workers.Go(func() {
			defer func() { <-s.slots }()
			s.handleTCP(ctx, conn)
		})
	}
}

func (s *state) handleTCP(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	for ctx.Err() == nil {
		if err := conn.SetReadDeadline(time.Now().Add(s.timeout)); err != nil {
			return
		}
		var prefix [2]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			return
		}
		packet := make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
		if _, err := io.ReadFull(conn, packet); err != nil {
			return
		}
		response, err := s.response(ctx, packet, false)
		if err != nil || len(response) > 65535 {
			return
		}
		binary.BigEndian.PutUint16(prefix[:], uint16(len(response)))
		if err := conn.SetWriteDeadline(time.Now().Add(s.timeout)); err != nil {
			return
		}
		// io.Copy handles partial writes; send prefix and message as one frame.
		frame := append(prefix[:], response...)
		if _, err := io.Copy(conn, bytes.NewReader(frame)); err != nil {
			return
		}
	}
}
