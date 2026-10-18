package webui

import (
	"context"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNotificationRendering(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, notice := range []string{"added", "updated", "deleted"} {
		t.Run(notice, func(t *testing.T) {
			response := send(h, http.MethodGet, "/?notice="+notice, nil)
			for _, want := range []string{
				`data-toast`, `role="status"`, `All set`,
				`aria-label="Dismiss notification" data-dismiss-toast hidden`,
				`<script src="/assets/notifications.js" defer></script>`,
				`<main id="main" tabindex="-1">`,
			} {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("missing notification markup %q", want)
				}
			}
			csp := response.Header().Get("Content-Security-Policy")
			if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
				t.Fatalf("unexpected CSP: %s", csp)
			}
		})
	}
	for _, path := range []string{"/", "/records/new", "/?notice=unknown"} {
		if response := send(h, http.MethodGet, path, nil); strings.Contains(response.Body.String(), "data-toast") {
			t.Fatalf("unexpected toast on %s", path)
		}
	}
	response := send(h, http.MethodGet, "/assets/notifications.js", nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || response.Body.String() != notificationScript {
		t.Fatal("notification script not served correctly")
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("notification script missing nosniff")
	}
	values := url.Values{"revision": {revision(z)}, "name": {"keep-this-name"}, "type": {"A"}, "ttl": {"60"}, "value": {"invalid-address"}}
	response = send(h, http.MethodPost, "/records", values)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `role="alert"`) || strings.Contains(response.Body.String(), "data-toast") {
		t.Fatal("validation error should remain inline, not become a toast")
	}
	for _, want := range []string{`value="keep-this-name"`, `invalid-address</textarea>`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("validation error lost form input %q", want)
		}
	}
}

// Node is optional: rendering and HTTP security tests above run with Go alone.
// This exercises the small DOM enhancement without any npm/browser dependencies.
func TestNotificationScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not available for notification behavior test")
	}
	const harness = `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const script = require("node:fs").readFileSync(0, "utf8");
for (const keyboardFocused of [false, true]) {
  const events = {};
  let removed = false, focused = false;
  const classes = [];
  const dismiss = { hidden: true, addEventListener: (name, callback) => { events[name] = callback; } };
  const toast = {
    querySelector: () => dismiss,
    remove: () => { removed = true; },
    classList: { add: (name) => classes.push(name) }
  };
  const main = { focus: (options) => { assert.equal(options.preventScroll, true); focused = true; } };
  const document = {
    querySelector: () => toast,
    getElementById: (id) => { assert.equal(id, "main"); return main; },
    activeElement: keyboardFocused ? dismiss : main
  };
  let replaced;
  const state = { saved: true };
  const window = {
    location: { href: "http://lantern.test:8080/?q=app&type=A&notice=deleted#reference" },
    history: { state, replaceState: (value, title, url) => { assert.equal(value, state); replaced = url; } }
  };
  vm.runInNewContext(script, { document, window, URL });
  assert.equal(dismiss.hidden, false);
  assert.deepEqual(classes, ["toast"]);
  assert.equal(removed, false); // No automatic dismissal or initial focus steal.
  assert.equal(focused, false);
  events.click(); // Native buttons also dispatch click for Enter and Space.
  assert.equal(removed, true);
  assert.equal(focused, keyboardFocused);
  assert.equal(replaced.href, "http://lantern.test:8080/?q=app&type=A#reference");
}
// Pages without notices and incomplete markup are safe no-ops.
vm.runInNewContext(script, { document: { querySelector: () => null } });
vm.runInNewContext(script, { document: { querySelector: () => ({ querySelector: () => null }) } });
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", harness)
	command.Stdin = strings.NewReader(notificationScript)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("notification behavior: %v\n%s", err, output)
	}
}
