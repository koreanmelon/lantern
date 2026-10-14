//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns"
	dnsserver "github.com/koreanmelon/lantern/pkg/dns/server"
	"golang.org/x/net/dns/dnsmessage"
)

func startTestServer(t *testing.T, s *dnsserver.Server) (string, context.CancelFunc) {
	t.Helper()
	var tcp net.Listener
	var udp net.PacketConn
	for range 20 {
		var err error
		tcp, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		udp, err = net.ListenPacket("udp4", tcp.Addr().String())
		if err == nil {
			break
		}
		tcp.Close()
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatal(err)
		}
	}
	if udp == nil {
		t.Fatal("could not allocate a shared UDP/TCP test port")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, udp, tcp) }()
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
	return tcp.Addr().String(), cancel
}

func TestAuthoritativeTransports(t *testing.T) {
	handler, err := dns.NewAuthoritative(newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		record("large.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{
			strings.Repeat("a", 250), strings.Repeat("b", 250), strings.Repeat("c", 250),
		}}),
	))
	if err != nil {
		t.Fatal(err)
	}
	address, _ := startTestServer(t, &dnsserver.Server{Handler: handler.Handle})
	for _, network := range []string{"udp", "tcp"} {
		for _, tc := range []struct {
			name    string
			typ     dnsmessage.Type
			code    dnsmessage.RCode
			answers int
		}{
			{"app.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 1},
			{"app.home.arpa.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 0},
			{"absent.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeNameError, 0},
			{"large.home.arpa.", dnsmessage.TypeTXT, dnsmessage.RCodeSuccess, 1},
		} {
			t.Run(network+"/"+tc.name+"/"+tc.typ.String(), func(t *testing.T) {
				query := dnsmessage.Message{
					ID:               42,
					RecursionDesired: true,
					Questions: []dnsmessage.Question{
						{
							Name:  dnsmessage.MustNewName(tc.name),
							Type:  tc.typ,
							Class: dnsmessage.ClassINET,
						},
					},
				}
				packet, err := query.Pack()
				if err != nil {
					t.Fatal(err)
				}
				conn, err := net.Dial(network, address)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				if network == "tcp" {
					var prefix [2]byte
					binary.BigEndian.PutUint16(prefix[:], uint16(len(packet)))
					if _, err := conn.Write(append(prefix[:], packet...)); err != nil {
						t.Fatal(err)
					}
					if _, err := io.ReadFull(conn, prefix[:]); err != nil {
						t.Fatal(err)
					}
					packet = make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
					if _, err := io.ReadFull(conn, packet); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := conn.Write(packet); err != nil {
						t.Fatal(err)
					}
					packet = make([]byte, 65535)
					n, err := conn.Read(packet)
					if err != nil {
						t.Fatal(err)
					}
					packet = packet[:n]
				}
				var response dnsmessage.Message
				if err := response.Unpack(packet); err != nil {
					t.Fatal(err)
				}
				if response.ID != 42 || !response.Authoritative || response.RecursionAvailable || response.RCode != tc.code {
					t.Fatalf("incorrect response: %s", response.GoString())
				}
				if network == "udp" && tc.name == "large.home.arpa." {
					if !response.Truncated || len(packet) > 512 {
						t.Fatal("oversized UDP answer not truncated")
					}
				} else if response.Truncated || len(response.Answers) != tc.answers {
					t.Fatal("incorrect complete answer")
				}
				if tc.answers == 0 && (len(response.Authorities) != 1 || response.Authorities[0].Header.TTL != 60) {
					t.Fatal("negative answer missing SOA")
				}
			})
		}
	}
}

func TestServerTransports(t *testing.T) {
	address, _ := startTestServer(t, &dnsserver.Server{})
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
				var message dnsmessage.Message
				if err := message.Unpack(response); err != nil {
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
	address, cancel := startTestServer(t, &dnsserver.Server{Timeout: time.Minute})
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
	s := &dnsserver.Server{Handler: func(ctx context.Context, query dnsmessage.Message) (dnsmessage.Message, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return dnsmessage.Message{}, ctx.Err()
	}}
	address, cancel := startTestServer(t, s)
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
	s := &dnsserver.Server{}
	t.Run("UDP only", func(t *testing.T) {
		udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer udp.Close()
		if err := s.Serve(t.Context(), udp, nil); err == nil {
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
		if err := s.Serve(t.Context(), nil, tcp); err == nil {
			t.Fatal("expected error")
		}
		if _, err := tcp.Accept(); err == nil {
			t.Fatal("listener still open")
		}
	})
}
