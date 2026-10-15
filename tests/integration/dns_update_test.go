//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

func TestLiveZoneReplacement(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			old := newTestZone(t, "home.arpa.", record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}))
			next := newTestZone(t, "home.arpa.", record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}))
			authority, err := zone.NewZoneHandler(old)
			if err != nil {
				t.Fatal(err)
			}
			address, _ := startTestServer(t, &dnsserver.Server{Config: dnsserver.Config{Handler: func(ctx context.Context, request dnsserver.Request) (dnsmessage.Message, error) {
				return authority.Handle(ctx, request.Message)
			}}})
			conn, err := net.Dial(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			query := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("app.home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
			request, err := query.Pack()
			if err != nil {
				t.Fatal(err)
			}
			// Keep both the server and client connection throughout the updates.
			for _, version := range []byte{1, 2, 0} {
				if version == 2 {
					if err := authority.ReplaceZones(next); err != nil {
						t.Fatal(err)
					}
				} else if version == 0 {
					if err := authority.ReplaceZones(); err != nil {
						t.Fatal(err)
					}
				}
				var packet []byte
				if network == "tcp" {
					var prefix [2]byte
					binary.BigEndian.PutUint16(prefix[:], uint16(len(request)))
					if _, err := conn.Write(append(prefix[:], request...)); err != nil {
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
					if _, err := conn.Write(request); err != nil {
						t.Fatal(err)
					}
					packet = make([]byte, 512)
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
				if version == 0 {
					if response.RCode != dnsmessage.RCodeRefused || response.Authoritative || len(response.Answers) != 0 {
						t.Fatal("removed zone is still served")
					}
					continue
				}
				if response.RCode != dnsmessage.RCodeSuccess || !response.Authoritative || len(response.Answers) != 1 {
					t.Fatal("missing authoritative answer")
				}
				body, ok := response.Answers[0].Body.(*dnsmessage.AResource)
				if !ok || body.A != [4]byte{192, 0, 2, version} {
					t.Fatal("update not visible on the existing connection")
				}
			}
		})
	}
}
