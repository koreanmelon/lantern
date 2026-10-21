package webui

import (
	"context"
	"math"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestThemeRendering(t *testing.T) {
	z := testZone(t)
	h, err := New(z, "lantern.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/records/new", "/?notice=deleted"} {
		page := send(h, http.MethodGet, path, nil)
		if page.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, page.Code)
		}
		body := page.Body.String()
		for _, want := range []string{
			`<meta name="color-scheme" content="light dark">`,
			`<script src="/assets/theme.js"></script>`,
			`<label class="theme-control" hidden><span class="sr-only">Color theme</span>`,
			`<option value="system">System</option>`,
			`<option value="light">Light</option>`,
			`<option value="dark">Dark</option>`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %s", path, want)
			}
		}
		if strings.Index(body, `/assets/theme.js`) > strings.Index(body, `/assets/style.css`) {
			t.Fatal("theme preference must apply before styles load")
		}
		if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-") {
			t.Fatalf("unexpected CSP: %s", csp)
		}
	}
	asset := send(h, http.MethodGet, "/assets/theme.js", nil)
	if asset.Code != http.StatusOK || asset.Body.String() != themeScript || asset.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || asset.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("theme asset response is incorrect")
	}
	css := send(h, http.MethodGet, "/assets/style.css", nil).Body.String()
	for _, want := range []string{`@media(prefers-color-scheme:dark){:root:not([data-theme="light"])`, `:root[data-theme="dark"]`, `color-scheme:dark`, `.theme-control[hidden]{display:none}`} {
		if !strings.Contains(css, want) {
			t.Fatalf("missing theme CSS %s", want)
		}
	}
}

func TestThemeScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not available for theme behavior test")
	}
	const harness = `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const script = require("node:fs").readFileSync(0, "utf8");
function start(saved, options = {}) {
  let stored = saved, lookups = 0;
  const events = {}, selectEvents = {}, windowEvents = {};
  const control = { hidden: true };
  const select = {
    value: "system",
    closest: (selector) => { assert.equal(selector, ".theme-control"); return control; },
    addEventListener: (name, fn) => { selectEvents[name] = fn; }
  };
  const document = {
    documentElement: { dataset: {} },
    addEventListener: (name, fn) => { events[name] = fn; },
    getElementById: (id) => { lookups++; assert.equal(id, "theme-preference"); return options.missing ? null : select; }
  };
  const window = {
    get localStorage() {
      if (options.blocked) throw new Error("storage denied");
      return {
        getItem: (key) => { assert.equal(key, "lantern-theme"); return stored; },
        setItem: (key, value) => {
          assert.equal(key, "lantern-theme");
          if (options.writeBlocked) throw new Error("quota exceeded");
          stored = value;
        }
      };
    },
    addEventListener: (name, fn) => { windowEvents[name] = fn; }
  };
  vm.runInNewContext(script, { document, window });
  assert.equal(lookups, 0); // Preference applies without waiting for body markup.
  assert.equal(control.hidden, true);
  const early = document.documentElement.dataset.theme;
  events.DOMContentLoaded();
  return { document, select, control, early, selectEvents, windowEvents, stored: () => stored };
}
for (const value of [null, "system", "light", "dark", "invalid", "<script>"]) {
  const expected = ["light", "dark"].includes(value) ? value : "system";
  const app = start(value);
  assert.equal(app.early, expected);
  assert.equal(app.select.value, expected);
  assert.equal(app.control.hidden, false);
  for (const choice of ["dark", "light", "system"]) {
    app.select.value = choice;
    app.selectEvents.change();
    assert.equal(app.document.documentElement.dataset.theme, choice);
    assert.equal(app.stored(), choice);
  }
  app.windowEvents.storage({ key: "lantern-theme", newValue: "dark" });
  assert.equal(app.select.value, "dark");
  app.windowEvents.storage({ key: "unrelated", newValue: "light" });
  assert.equal(app.select.value, "dark");
  app.windowEvents.storage({ key: null, newValue: null });
  assert.equal(app.select.value, "system");
}
for (const options of [{ blocked: true }, { writeBlocked: true }]) {
  const app = start(null, options);
  assert.equal(app.early, "system");
  app.select.value = "dark";
  app.selectEvents.change();
  assert.equal(app.document.documentElement.dataset.theme, "dark");
}
assert.equal(start("dark").early, "dark"); // Restored on a subsequent page load.
assert.equal(start("dark", { missing: true }).control.hidden, true);
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", harness)
	command.Stdin = strings.NewReader(themeScript)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("theme behavior: %v\n%s", err, output)
	}
}

func TestDarkThemeTextContrast(t *testing.T) {
	colors := map[string]string{"white": "#ffffff"}
	for _, match := range regexp.MustCompile(`(--[a-z-]+):(#\w{6});`).FindAllStringSubmatch(darkPalette, -1) {
		colors[match[1]] = match[2]
	}
	luminance := func(hex string) float64 {
		t.Helper()
		if len(hex) != 7 {
			t.Fatalf("invalid palette color %q", hex)
		}
		var result float64
		for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
			channel, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
			if err != nil {
				t.Fatal(err)
			}
			value := float64(channel) / 255
			if value <= 0.04045 {
				value /= 12.92
			} else {
				value = math.Pow((value+0.055)/1.055, 2.4)
			}
			result += value * weight
		}
		return result
	}
	for _, pair := range [][2]string{
		{"--ink", "--bg"}, {"--ink", "--surface"}, {"--muted", "--surface"},
		{"--theme-secondary", "--theme-input"}, {"--theme-faint", "--surface"},
		{"--blue", "--blue-soft"}, {"--theme-secondary", "--theme-badge"},
		{"--theme-success-ink", "--theme-success-bg"}, {"--theme-error-ink", "--theme-error-bg"},
		{"--theme-a-ink", "--theme-a-bg"}, {"--theme-aaaa-ink", "--theme-aaaa-bg"},
		{"--theme-alias-ink", "--theme-alias-bg"}, {"--theme-mail-ink", "--theme-mail-bg"},
		{"--theme-txt-ink", "--theme-txt-bg"},
		{"white", "--theme-primary"}, {"white", "--theme-primary-hover"},
	} {
		fg, bg := luminance(colors[pair[0]]), luminance(colors[pair[1]])
		contrast := (math.Max(fg, bg) + 0.05) / (math.Min(fg, bg) + 0.05)
		if contrast < 4.5 {
			t.Errorf("%s on %s contrast %.2f, want at least 4.5", pair[0], pair[1], contrast)
		}
	}
}
