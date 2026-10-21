package webui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCompactHeaderLayout(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	edit := fmt.Sprintf("/records/%d/edit", indexOf(t, z, "app.home.arpa.", dnsmessage.TypeA))
	for _, path := range []string{"/", "/records/new", edit} {
		t.Run(path, func(t *testing.T) {
			response := send(h, http.MethodGet, path, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, obsolete := range []string{`class="sidebar"`, `aria-label="Main navigation"`, `href="/#reference"`, `class="breadcrumb"`} {
				if strings.Contains(body, obsolete) {
					t.Fatalf("obsolete sidebar navigation: %s", obsolete)
				}
			}
			_, header, found := strings.Cut(body, `<header class="page-header">`)
			if !found {
				t.Fatal("missing compact header")
			}
			header, _, found = strings.Cut(header, "</header>")
			if !found {
				t.Fatal("unclosed header")
			}
			for _, want := range []string{`href="/" aria-label="Lantern home"`, `CURRENT ZONE`, `home.arpa.`, `Authoritative · IN class`, `id="theme-preference"`, `No authentication is enabled.`, `Keep this dashboard on a trusted interface.`} {
				if !strings.Contains(header, want) {
					t.Fatalf("header missing %s", want)
				}
			}
			if strings.Count(body, `id="theme-preference"`) != 1 {
				t.Fatal("theme selector must appear exactly once")
			}
			if !strings.Contains(body, `<details id="reference"`) || !strings.Contains(body, `Record format guide`) {
				t.Fatal("record format guide was removed")
			}
			if path == "/" && !strings.Contains(body, `href="/records/new"><span>＋</span> Add record`) {
				t.Fatal("missing Add record action")
			}
			if path != "/" && !strings.Contains(body, `← All records`) {
				t.Fatal("missing return to records link")
			}
		})
	}
}
