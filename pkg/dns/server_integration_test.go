//go:build integration

package dns

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func startTestServer(t *testing.T, server *Server) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp4", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, udp, tcp) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server failed to stop")
		}
	})
	return tcp.Addr().String(), cancel, done
}

func TestServerTransports(t *testing.T) {
	address, _, _ := startTestServer(t, &Server{})
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			conn, err := net.Dial(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			// Reuse a TCP connection for two complete exchanges.
			for range 2 {
				query := testQuery(t)
				packet, err := query.Pack()
				if err != nil {
					t.Fatal(err)
				}
				var response []byte
				if network == "tcp" {
					var prefix [2]byte
					binary.BigEndian.PutUint16(prefix[:], uint16(len(packet)))
					// Split the prefix to exercise stream framing.
					if _, err := conn.Write(prefix[:1]); err != nil {
						t.Fatal(err)
					}
					if _, err := conn.Write(append(prefix[1:], packet...)); err != nil {
						t.Fatal(err)
					}
					if _, err := io.ReadFull(conn, prefix[:]); err != nil {
						t.Fatal(err)
					}
					response = make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
					if _, err := io.ReadFull(conn, response); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := conn.Write(packet); err != nil {
						t.Fatal(err)
					}
					response = make([]byte, 65535)
					n, err := conn.Read(response)
					if err != nil {
						t.Fatal(err)
					}
					response = response[:n]
				}
				message, err := Parse(response)
				if err != nil {
					t.Fatal(err)
				}
				if message.ID != query.ID || !message.Response || message.RCode != dnsmessage.RCodeRefused {
					t.Fatalf("unexpected response: %s", message.GoString())
				}
			}
		})
	}
}

func TestServerShutdownClosesIdleTCP(t *testing.T) {
	address, cancel, _ := startTestServer(t, &Server{Timeout: time.Minute})
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	_, err = conn.Read(b[:])
	if err == nil {
		t.Fatal("connection remained open")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("shutdown did not close connection")
	}
}

func TestServerCancelsActiveHandler(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	server := &Server{Handler: HandlerFunc(func(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return dnsmessage.Message{}, ctx.Err()
	})}
	address, cancel, _ := startTestServer(t, server)
	conn, err := net.Dial("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := testQuery(t)
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handler was not canceled")
	}
}

func TestServeClosesPartiallyProvidedListeners(t *testing.T) {
	server := &Server{}
	t.Run("UDP only", func(t *testing.T) {
		udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer udp.Close()
		if err := server.Serve(t.Context(), udp, nil); err == nil {
			t.Fatal("expected error")
		}
		if _, _, err := udp.ReadFrom(make([]byte, 1)); err == nil {
			t.Fatal("listener still open")
		}
	})
	t.Run("TCP only", func(t *testing.T) {
		tcp, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer tcp.Close()
		if err := server.Serve(t.Context(), nil, tcp); err == nil {
			t.Fatal("expected error")
		}
		if _, err := tcp.Accept(); err == nil {
			t.Fatal("listener still open")
		}
	})
}
