//go:build integration

package integration

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"github.com/koreanmelon/lantern/pkg/webui"
	"golang.org/x/net/dns/dnsmessage"
)

func TestWebEditReachesDNS(t *testing.T) {
	z := newTestZone(t, "home.arpa.")
	dnsAddress, _ := startTestServer(t, &dnsserver.Server{Config: dnsserver.Config{Handler: z.Handle}})
	handler, err := webui.New(z)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	_, revision := z.Records()
	values := url.Values{"revision": {strconv.FormatUint(revision, 10)}, "name": {"web"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.44"}}
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/records", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", httpServer.URL)
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("web save: %d %s", response.StatusCode, body)
	}
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			conn, err := net.Dial(network, dnsAddress)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			query := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("web.home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
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
				n, err := conn.Read(packet)
				if err != nil {
					t.Fatal(err)
				}
				packet = packet[:n]
			}
			var answer dnsmessage.Message
			if err := answer.Unpack(packet); err != nil {
				t.Fatal(err)
			}
			if answer.RCode != dnsmessage.RCodeSuccess || !answer.Authoritative || len(answer.Answers) != 1 {
				t.Fatal("missing live DNS answer")
			}
			if body, ok := answer.Answers[0].Body.(*dnsmessage.AResource); !ok || body.A != [4]byte{192, 0, 2, 44} {
				t.Fatal("HTTP mutation did not reach DNS")
			}
		})
	}
}
