//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// TestCompiledAppPersistence is the end-to-end smoke test used locally and by
// CI. It never uses the caller's record file, installed binary, or systemd unit.
func TestCompiledAppPersistence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix process signals")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "lantern")
	buildCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-trimpath", "-o", binary, "./cmd/lantern")
	build.WaitDelay = 2 * time.Second
	build.Dir = root
	build.Env = append(smokeEnv(), "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile app: %v\n%s", err, output)
	}
	state := filepath.Join(dir, "state", "records.json")
	first := startCompiledApp(t, binary, state, root)
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	version := first.revision(t)
	if response := first.save(t, url.Values{"revision": {version}, "name": {"invalid"}, "type": {"TXT"}, "ttl": {"60"}, "value": {`["hello",null]`}}); response != http.StatusUnprocessableEntity {
		t.Fatalf("invalid TXT status: %d", response)
	}
	after, err := os.ReadFile(state)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid form changed the record file")
	}
	for _, record := range []struct{ name, typ, value string }{
		{"persisted", "A", "192.0.2.77"},
		{"persisted-txt", "TXT", `["hello", "line\u0000break"]`},
	} {
		if code := first.save(t, url.Values{"revision": {first.revision(t)}, "name": {record.name}, "type": {record.typ}, "ttl": {"60"}, "value": {record.value}}); code != http.StatusSeeOther {
			t.Fatalf("save %s: HTTP %d", record.name, code)
		}
	}
	assertSmokeAnswers(t, first)
	oldRevision := first.revision(t)
	first.stop(t)
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Version  int               `json:"version"`
		Origin   string            `json:"origin"`
		Revision uint64            `json:"revision"`
		Records  []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Version != 1 || stored.Origin != "home.arpa." || len(stored.Records) < 4 || strconv.FormatUint(stored.Revision, 10) != oldRevision {
		t.Fatalf("unexpected saved state: %+v", stored)
	}
	second := startCompiledApp(t, binary, state, root)
	assertSmokeAnswers(t, second)
	newRevision := second.revision(t)
	old, _ := strconv.ParseUint(oldRevision, 10, 64)
	current, _ := strconv.ParseUint(newRevision, 10, 64)
	if current <= old {
		t.Fatal("restart did not advance the browser revision")
	}
	if code := second.save(t, url.Values{"revision": {oldRevision}, "name": {"stale"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.99"}}); code != http.StatusConflict {
		t.Fatalf("pre-restart form was not rejected: %d", code)
	}
	if second.revision(t) != newRevision {
		t.Fatal("rejected stale form changed the revision")
	}
	second.stop(t)
	t.Log("compiled-app HTTP edits, UDP/TCP queries, SIGTERM restart, JSON reload, and stale-form protection passed")
}

type compiledApp struct {
	cmd                *exec.Cmd
	done               <-chan error
	stderr             *bytes.Buffer
	waited             bool
	waitErr            error
	webURL, dnsAddress string
	client             *http.Client
}

func smokeEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "LANTERN_") || key == "CGO_ENABLED" || key == "GOOS" || key == "GOARCH" || key == "GOARM" {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func startCompiledApp(t *testing.T, binary, state, root string) *compiledApp {
	t.Helper()
	// The command accepts addresses rather than inherited listeners, so reserve
	// and release ephemeral ports. Retry only a genuine startup bind collision.
	for attempt := 0; attempt < 5; attempt++ {
		udp, tcp := listenTestPair(t)
		dnsAddress := tcp.Addr().String()
		web, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			udp.Close()
			tcp.Close()
			t.Fatal(err)
		}
		webAddress := web.Addr().String()
		udp.Close()
		tcp.Close()
		web.Close()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		cmd := exec.CommandContext(ctx, binary, "--config", filepath.Join(root, "configs", "lantern.yaml"), "--dns-listen", dnsAddress, "--web-listen", webAddress)
		// Use an existing unrelated working directory; records_file is absolute.
		cmd.Dir = filepath.Dir(filepath.Dir(state))
		cmd.Env = append(smokeEnv(), "LANTERN_RECORDS_FILE="+state)
		cmd.WaitDelay = 2 * time.Second
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		app := &compiledApp{cmd: cmd, done: done, stderr: stderr, webURL: "http://" + webAddress, dnsAddress: dnsAddress,
			client: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		t.Cleanup(func() { app.killAndWait(); app.client.CloseIdleConnections(); cancel() })
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if app.exited() {
				break
			}
			response, err := app.client.Get(app.webURL)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				response.Body.Close()
				if readErr == nil && response.StatusCode == http.StatusOK && bytes.Contains(body, []byte(`data-storage="file"`)) {
					answer, err := exchangeSmokeDNS(t.Context(), "udp", dnsAddress, "home.arpa.", dnsmessage.TypeSOA)
					if err == nil && answer.Authoritative && len(answer.Answers) == 1 {
						return app
					}
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		app.killAndWait()
		cancel()
		if app.waited && strings.Contains(app.stderr.String(), "address already in use") {
			continue
		}
		if app.waited {
			t.Fatalf("app did not become ready: %v\n%s", app.waitErr, app.stderr.String())
		}
		t.Fatal("app did not become ready or exit within its deadline")
	}
	t.Fatal("could not start app after ephemeral-port collisions")
	return nil
}

func (a *compiledApp) exited() bool {
	if !a.waited {
		select {
		case a.waitErr = <-a.done:
			a.waited = true
		default:
		}
	}
	return a.waited
}

func (a *compiledApp) killAndWait() {
	if a.exited() {
		return
	}
	_ = a.cmd.Process.Kill()
	select {
	case a.waitErr = <-a.done:
		a.waited = true
	case <-time.After(3 * time.Second):
	}
}

func (a *compiledApp) stop(t *testing.T) {
	t.Helper()
	if a.exited() {
		t.Fatalf("app exited early: %v\n%s", a.waitErr, a.stderr.String())
	}
	if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	select {
	case a.waitErr = <-a.done:
		a.waited = true
	case <-time.After(8 * time.Second):
		a.killAndWait()
		t.Fatal("app did not stop gracefully")
	}
	if a.waitErr != nil {
		t.Fatalf("app shutdown: %v\n%s", a.waitErr, a.stderr.String())
	}
}

func (a *compiledApp) revision(t *testing.T) string {
	t.Helper()
	response, err := a.client.Get(a.webURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("read dashboard: %v, status=%d", err, response.StatusCode)
	}
	match := regexp.MustCompile(`name="revision" value="([0-9]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("dashboard has no revision")
	}
	return string(match[1])
}

func (a *compiledApp) save(t *testing.T, values url.Values) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, a.webURL+"/records", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", a.webURL)
	response, err := a.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode
}

func assertSmokeAnswers(t *testing.T, app *compiledApp) {
	t.Helper()
	for _, network := range []string{"udp", "tcp"} {
		answer, err := exchangeSmokeDNS(t.Context(), network, app.dnsAddress, "persisted.home.arpa.", dnsmessage.TypeA)
		if err != nil {
			t.Fatal(err)
		}
		if !answer.Authoritative || answer.RCode != dnsmessage.RCodeSuccess || len(answer.Answers) != 1 {
			t.Fatalf("%s: incorrect A response", network)
		}
		if body, ok := answer.Answers[0].Body.(*dnsmessage.AResource); !ok || body.A != [4]byte{192, 0, 2, 77} {
			t.Fatalf("%s: wrong saved address", network)
		}
		answer, err = exchangeSmokeDNS(t.Context(), network, app.dnsAddress, "persisted-txt.home.arpa.", dnsmessage.TypeTXT)
		if err != nil {
			t.Fatal(err)
		}
		if answer.RCode != dnsmessage.RCodeSuccess || len(answer.Answers) != 1 {
			t.Fatalf("%s: missing TXT response", network)
		}
		body, ok := answer.Answers[0].Body.(*dnsmessage.TXTResource)
		if !ok || len(body.TXT) != 2 || body.TXT[0] != "hello" || body.TXT[1] != "line\x00break" {
			t.Fatalf("%s: TXT bytes changed", network)
		}
	}
}

func exchangeSmokeDNS(ctx context.Context, network, address, name string, typ dnsmessage.Type) (dnsmessage.Message, error) {
	var answer dnsmessage.Message
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 42}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}}}
	packet, err := query.Pack()
	if err != nil {
		return answer, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return answer, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return answer, err
	}
	if network == "tcp" {
		var prefix [2]byte
		binary.BigEndian.PutUint16(prefix[:], uint16(len(packet)))
		if _, err := conn.Write(append(prefix[:], packet...)); err != nil {
			return answer, err
		}
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			return answer, err
		}
		packet = make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
		if _, err := io.ReadFull(conn, packet); err != nil {
			return answer, err
		}
	} else {
		if _, err := conn.Write(packet); err != nil {
			return answer, err
		}
		packet = make([]byte, 65535)
		n, err := conn.Read(packet)
		if err != nil {
			return answer, err
		}
		packet = packet[:n]
	}
	if err := answer.Unpack(packet); err != nil {
		return answer, err
	}
	if answer.ID != query.ID || !answer.Response {
		return answer, fmt.Errorf("invalid DNS response envelope")
	}
	return answer, nil
}
