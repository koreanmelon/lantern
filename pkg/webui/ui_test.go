package webui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/html"
)

func testZone(t *testing.T) *zone.Zone {
	t.Helper()
	r := func(name string, typ dnsmessage.Type, body dnsmessage.ResourceBody) dnsmessage.Resource {
		return dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET, TTL: 300}, Body: body}
	}
	z, err := zone.NewZone("home.arpa.", []dnsmessage.Resource{
		r("home.arpa.", dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns.home.arpa."), MBox: dnsmessage.MustNewName("hostmaster.home.arpa."), Serial: 1, Refresh: 3600, Retry: 600, Expire: 86400, MinTTL: 60}),
		r("home.arpa.", dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.home.arpa.")}),
		r("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		r("info.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"hello"}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func revision(z *zone.Zone) string {
	_, version := z.Records()
	return strconv.FormatUint(version, 10)
}

func send(h http.Handler, method, path string, values url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://lantern.test"+path, strings.NewReader(values.Encode()))
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func indexOf(t *testing.T, z *zone.Zone, name string, typ dnsmessage.Type) int {
	t.Helper()
	records, _ := z.Records()
	for id, record := range records {
		if record.Header.Name.String() == name && record.Header.Type == typ {
			return id
		}
	}
	t.Fatalf("record not found: %s %v", name, typ)
	return -1
}

func TestPagesAndRecordWorkflow(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, text string }{
		{"/", "DNS records"}, {"/records/new", "Add a record"},
		{"/?q=app&type=A", "1 of 4 records"}, {"/assets/style.css", "@media"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := send(h, http.MethodGet, tc.path, nil)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), tc.text) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing CSP")
			}
		})
	}
	values := url.Values{"revision": {revision(z)}, "name": {"nas"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.10"}}
	if response := send(h, http.MethodPost, "/records", values); response.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	id := indexOf(t, z, "nas.home.arpa.", dnsmessage.TypeA)
	path := fmt.Sprintf("/records/%d/edit", id)
	if response := send(h, http.MethodGet, path+"?revision="+revision(z), nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "192.0.2.10") {
		t.Fatalf("edit page: %d %s", response.Code, response.Body.String())
	}
	values.Set("revision", revision(z))
	values.Set("value", "192.0.2.20")
	if response := send(h, http.MethodPost, path, values); response.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", response.Code, response.Body.String())
	}
	answer, err := z.Handle(context.Background(), dnsserver.Request{Message: dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("nas.home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}})
	if err != nil || len(answer.Answers) != 1 || answer.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{192, 0, 2, 20} {
		t.Fatal("web edit did not update live DNS data")
	}
	id = indexOf(t, z, "nas.home.arpa.", dnsmessage.TypeA)
	if response := send(h, http.MethodPost, fmt.Sprintf("/records/%d/delete", id), url.Values{"revision": {revision(z)}}); response.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if _, exists := z.Lookup("nas.home.arpa.", dnsmessage.TypeA); exists {
		t.Fatal("deleted record still exists")
	}
}

func TestInvalidEditsAndStaleTabs(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	initial := revision(z)
	for _, tc := range []struct{ name, typ, value string }{
		{"outside.arpa.", "A", "192.0.2.1"},
		{"app", "CNAME", "target.home.arpa."},
		{"bad", "A", "not an IP"},
		{"bad", "UNKNOWN", "value"},
	} {
		t.Run(tc.name+tc.typ, func(t *testing.T) {
			response := send(h, http.MethodPost, "/records", url.Values{"revision": {initial}, "name": {tc.name}, "type": {tc.typ}, "ttl": {"60"}, "value": {tc.value}})
			if response.Code != http.StatusUnprocessableEntity || revision(z) != initial {
				t.Fatalf("invalid edit: status=%d revision=%s", response.Code, revision(z))
			}
		})
	}
	soa := indexOf(t, z, "home.arpa.", dnsmessage.TypeSOA)
	response := send(h, http.MethodPost, fmt.Sprintf("/records/%d/delete", soa), url.Values{"revision": {initial}})
	if response.Code != http.StatusUnprocessableEntity || revision(z) != initial {
		t.Fatal("essential SOA deletion was not rejected")
	}
	if err := z.Add(dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("new.home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{}}); err != nil {
		t.Fatal(err)
	}
	values := url.Values{"revision": {initial}, "name": {"stale"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.2"}}
	if response := send(h, http.MethodPost, "/records", values); response.Code != http.StatusConflict {
		t.Fatalf("stale edit: %d", response.Code)
	}
	if response := send(h, http.MethodGet, fmt.Sprintf("/records/%d/edit?revision=%s", soa, initial), nil); response.Code != http.StatusConflict {
		t.Fatalf("stale link: %d", response.Code)
	}
	if _, exists := z.Lookup("stale.home.arpa.", dnsmessage.TypeA); exists {
		t.Fatal("stale edit was committed")
	}
}

func TestConcurrentWebEdits(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	version := revision(z)
	results := make(chan int, 2)
	var workers sync.WaitGroup
	for i := range 2 {
		workers.Go(func() {
			values := url.Values{"revision": {version}, "name": {fmt.Sprintf("client%d", i)}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.1"}}
			results <- send(h, http.MethodPost, "/records", values).Code
		})
	}
	workers.Wait()
	close(results)
	counts := map[int]int{}
	for result := range results {
		counts[result]++
	}
	if counts[http.StatusSeeOther] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("unexpected concurrent results: %v", counts)
	}
	records, _ := z.Records()
	if len(records) != 5 {
		t.Fatal("concurrent edit lost or duplicated records")
	}
}

func TestWebBoundariesAndEscaping(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []struct{ key, value string }{{"Origin", "https://evil.example"}, {"Sec-Fetch-Site", "cross-site"}} {
		request := httptest.NewRequest(http.MethodPost, "http://lantern.test/records", nil)
		request.Header.Set(header.key, header.value)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("cross-origin request accepted: %d", response.Code)
		}
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/records/999/edit", http.StatusNotFound},
		{http.MethodGet, "/records/0/delete", http.StatusMethodNotAllowed},
		{http.MethodGet, "/missing", http.StatusNotFound},
		{http.MethodPost, "/records", http.StatusBadRequest},
	} {
		if response := send(h, tc.method, tc.path, nil); response.Code != tc.code {
			t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
		}
	}
	values := url.Values{"revision": {revision(z)}, "name": {"text"}, "type": {"TXT"}, "ttl": {"60"}, "value": {"<script>alert('x')</script>"}}
	if response := send(h, http.MethodPost, "/records", values); response.Code != http.StatusSeeOther {
		t.Fatalf("TXT: %d %s", response.Code, response.Body.String())
	}
	response := send(h, http.MethodGet, "/", nil)
	if strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), "&lt;script&gt;") {
		t.Fatal("record data was not HTML-escaped")
	}
	values.Set("value", strings.Repeat("x", 70<<10))
	if response := send(h, http.MethodPost, "/records", values); response.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: %d", response.Code)
	}
	if _, err := New(nil); err == nil {
		t.Fatal("nil zone accepted")
	}
	if _, err := New(&zone.Zone{}); err == nil {
		t.Fatal("uninitialized zone accepted")
	}
}

func TestHostValidation(t *testing.T) {
	z := testZone(t)
	h, err := New(z)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		code int
	}{
		{"127.0.0.1:8080", http.StatusOK}, {"localhost:8080", http.StatusOK}, {"[::1]:8080", http.StatusOK},
		{"attacker.example:8080", http.StatusMisdirectedRequest},
	} {
		t.Run(tc.host, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/", nil)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Origin", "http://"+tc.host)
			request.Header.Set("X-Forwarded-Host", "localhost:8080")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != tc.code {
				t.Fatalf("Host %q: got %d", tc.host, response.Code)
			}
		})
	}
	version := revision(z)
	values := url.Values{"revision": {version}, "name": {"rebound"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.1"}}
	request := httptest.NewRequest(http.MethodPost, "http://attacker.example:8080/records", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Origin", "http://attacker.example:8080")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusMisdirectedRequest || revision(z) != version {
		t.Fatal("rebound host was allowed to mutate records")
	}
}

func TestTXTEditPreservesNUL(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"revision": {revision(z)}, "name": {"binary"}, "type": {"TXT"}, "ttl": {"60"}, "value": {`["before\u0000after"]`}}
	if response := send(h, http.MethodPost, "/records", values); response.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	id := indexOf(t, z, "binary.home.arpa.", dnsmessage.TypeTXT)
	path := fmt.Sprintf("/records/%d/edit", id)
	page := send(h, http.MethodGet, path, nil)
	if page.Code != http.StatusOK {
		t.Fatal(page.Body.String())
	}
	// HTML parsing models entity decoding and NUL replacement at the browser
	// boundary rather than just testing the Go formatter and parser together.
	doc, err := html.Parse(strings.NewReader(page.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var value string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "textarea" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				value += child.Data
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	values.Set("value", value)
	values.Set("revision", revision(z))
	values.Set("ttl", "120")
	if response := send(h, http.MethodPost, path, values); response.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", response.Code, response.Body.String())
	}
	records, _ := z.Lookup("binary.home.arpa.", dnsmessage.TypeTXT)
	if records[0].Body.(*dnsmessage.TXTResource).TXT[0] != "before\x00after" {
		t.Fatal("HTML edit changed the TXT payload")
	}
}

func TestNonUTF8EditIsRefused(t *testing.T) {
	z := testZone(t)
	if err := z.Add(dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("binary.home.arpa."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.TXTResource{TXT: []string{"\xff"}}}); err != nil {
		t.Fatal(err)
	}
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	id := indexOf(t, z, "binary.home.arpa.", dnsmessage.TypeTXT)
	if response := send(h, http.MethodGet, fmt.Sprintf("/records/%d/edit", id), nil); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("binary edit accepted: %d", response.Code)
	}
}

func TestStaleIndexedMutationDoesNotTouchAnotherRecord(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	id := indexOf(t, z, "app.home.arpa.", dnsmessage.TypeA)
	oldVersion := revision(z)
	if err := z.Add(dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("aaa.home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{}}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"edit", "delete"} {
		values := url.Values{"revision": {oldVersion}, "name": {"app"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.99"}}
		response := send(h, http.MethodPost, fmt.Sprintf("/records/%d/%s", id, action), values)
		if response.Code != http.StatusConflict {
			t.Fatalf("stale %s: %d", action, response.Code)
		}
	}
	for _, name := range []string{"aaa.home.arpa.", "app.home.arpa."} {
		if records, _ := z.Lookup(name, dnsmessage.TypeA); len(records) != 1 {
			t.Fatalf("stale mutation changed %s", name)
		}
	}
}

func TestWebPersistenceAndFailedSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	z := testZone(t)
	if err := z.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	page := send(h, http.MethodGet, "/", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Saved before it goes live") {
		t.Fatal("persistent status not shown")
	}
	values := url.Values{"revision": {revision(z)}, "name": {"saved"}, "type": {"A"}, "ttl": {"60"}, "value": {"192.0.2.8"}}
	if response := send(h, http.MethodPost, "/records", values); response.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	restored := testZone(t)
	if err := restored.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if records, _ := restored.Lookup("saved.home.arpa.", dnsmessage.TypeA); len(records) != 1 || records[0].Body.(*dnsmessage.AResource).A[3] != 8 {
		t.Fatal("web edit did not survive reload")
	}
	// Use only the restored zone from here: one live owner per record file.
	h, err = New(restored, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	version := revision(restored)
	if err := os.Rename(path, path+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	values.Set("revision", version)
	values.Set("name", "unsaved")
	response := send(h, http.MethodPost, "/records", values)
	if response.Code != http.StatusUnprocessableEntity || revision(restored) != version {
		t.Fatal("failed disk save was reported as success")
	}
	if _, exists := restored.Lookup("unsaved.home.arpa.", dnsmessage.TypeA); exists {
		t.Fatal("failed disk save changed live DNS data")
	}
}
