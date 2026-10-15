//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"golang.org/x/net/dns/dnsmessage"
)

func TestHandlerClientIP(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			requests := make(chan dnsserver.Request, 1)
			s := &dnsserver.Server{Config: dnsserver.Config{Handler: func(_ context.Context, request dnsserver.Request) (dnsmessage.Message, error) {
				requests <- request
				return dnsmessage.Message{}, nil
			}}}
			address, _ := startTestServer(t, s)
			conn, err := net.Dial(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			query := testQuery(t)
			packet, err := query.Pack()
			if err != nil {
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
				packet = make([]byte, 512)
				if _, err := conn.Read(packet); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case request := <-requests:
				if request.ClientIP != netip.MustParseAddr("127.0.0.1") || request.Message.ID != query.ID ||
					len(request.Message.Questions) != 1 || request.Message.Questions[0] != query.Questions[0] {
					t.Fatalf("incorrect handler request: %+v", request)
				}
			default:
				t.Fatal("response arrived without handler invocation")
			}
		})
	}
}
