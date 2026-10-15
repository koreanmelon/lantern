package dnsserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// These in-memory sockets expose deadlines without depending on wall-clock
// sleeps or real network traffic.
type memoryConn struct {
	input  *bytes.Reader
	output bytes.Buffer
	reads  []time.Time
	writes []time.Time
}

func (c *memoryConn) Read(p []byte) (int, error)  { return c.input.Read(p) }
func (c *memoryConn) Write(p []byte) (int, error) { return c.output.Write(p) }
func (c *memoryConn) Close() error                { return nil }
func (c *memoryConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 53}
}
func (c *memoryConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.2"), Port: 1234}
}
func (c *memoryConn) SetDeadline(t time.Time) error {
	c.reads = append(c.reads, t)
	c.writes = append(c.writes, t)
	return nil
}
func (c *memoryConn) SetReadDeadline(t time.Time) error  { c.reads = append(c.reads, t); return nil }
func (c *memoryConn) SetWriteDeadline(t time.Time) error { c.writes = append(c.writes, t); return nil }

func TestIndependentTCPTimeouts(t *testing.T) {
	query := testQuery(t)
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var prefix [2]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(packet)))
	conn := &memoryConn{input: bytes.NewReader(append(prefix[:], packet...))}
	started := time.Now()
	called := false
	s := Server{Config: Config{
		ReadTimeout: time.Hour, WriteTimeout: 2 * time.Hour, HandlerTimeout: 3 * time.Hour,
		Handler: func(ctx context.Context, request Request) (dnsmessage.Message, error) {
			called = true
			if request.ClientIP != netip.MustParseAddr("192.0.2.2") {
				t.Fatal("wrong TCP client IP")
			}
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Before(started.Add(3*time.Hour)) || deadline.After(time.Now().Add(3*time.Hour)) {
				t.Fatal("wrong handler deadline")
			}
			return dnsmessage.Message{}, nil
		},
	}}
	s.handleTCP(t.Context(), conn)
	if !called || len(conn.reads) != 2 || len(conn.writes) != 1 {
		t.Fatal("incomplete TCP exchange")
	}
	for _, deadline := range conn.reads {
		if deadline.Before(started.Add(time.Hour)) || deadline.After(time.Now().Add(time.Hour)) {
			t.Fatal("read deadline did not use ReadTimeout")
		}
	}
	if conn.writes[0].Before(started.Add(2*time.Hour)) || conn.writes[0].After(time.Now().Add(2*time.Hour)) {
		t.Fatal("write deadline did not use WriteTimeout")
	}
	encoded := conn.output.Bytes()
	if len(encoded) < 2 || int(binary.BigEndian.Uint16(encoded[:2])) != len(encoded)-2 {
		t.Fatal("incorrect response framing")
	}
}

type memoryPacketConn struct {
	deadline time.Time
	data     []byte
	address  net.Addr
}

func (c *memoryPacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, io.EOF }
func (c *memoryPacketConn) WriteTo(p []byte, address net.Addr) (int, error) {
	c.data = append([]byte(nil), p...)
	c.address = address
	return len(p), nil
}
func (c *memoryPacketConn) Close() error                       { return nil }
func (c *memoryPacketConn) LocalAddr() net.Addr                { return &net.UDPAddr{} }
func (c *memoryPacketConn) SetDeadline(t time.Time) error      { c.deadline = t; return nil }
func (c *memoryPacketConn) SetReadDeadline(time.Time) error    { return nil }
func (c *memoryPacketConn) SetWriteDeadline(t time.Time) error { c.deadline = t; return nil }

func TestUDPWriteTimeout(t *testing.T) {
	s := Server{Config: Config{ReadTimeout: time.Hour, WriteTimeout: 2 * time.Hour, HandlerTimeout: 3 * time.Hour}}
	conn := &memoryPacketConn{}
	address := &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234}
	started := time.Now()
	if err := s.writeUDP(t.Context(), conn, []byte("response"), address); err != nil {
		t.Fatal(err)
	}
	if string(conn.data) != "response" || conn.address != address || conn.deadline.Before(started.Add(2*time.Hour)) || conn.deadline.After(time.Now().Add(2*time.Hour)) {
		t.Fatal("UDP send did not preserve the payload/address or apply WriteTimeout")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.writeUDP(ctx, conn, []byte("canceled"), address); err != context.Canceled {
		t.Fatalf("got %v, want cancellation", err)
	}
	if string(conn.data) != "response" {
		t.Fatal("sent data after cancellation")
	}
}

type blockingPacketConn struct {
	memoryPacketConn
	entered    chan struct{}
	release    chan struct{}
	writeCount int
}

func (c *blockingPacketConn) WriteTo(p []byte, address net.Addr) (int, error) {
	c.writeCount++
	close(c.entered)
	<-c.release
	return c.memoryPacketConn.WriteTo(p, address)
}

func TestUDPWriteLockCoversSend(t *testing.T) {
	s := Server{Config: Config{WriteTimeout: time.Second}}
	conn := &blockingPacketConn{entered: make(chan struct{}), release: make(chan struct{})}
	address := &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234}
	first := make(chan error, 1)
	go func() { first <- s.writeUDP(t.Context(), conn, []byte("first"), address) }()
	<-conn.entered
	// SetWriteDeadline and the entire WriteTo must stay in one critical section.
	locked := s.udpWrites.TryLock()
	if locked {
		s.udpWrites.Unlock()
		t.Error("UDP send does not hold the deadline lock")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	queued := make(chan error, 1)
	go func() { queued <- s.writeUDP(ctx, conn, []byte("queued"), address) }()
	close(conn.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-queued; err != context.Canceled {
		t.Fatalf("queued write: got %v", err)
	}
	if conn.writeCount != 1 || string(conn.data) != "first" {
		t.Fatal("canceled queued writer sent a response")
	}
}
