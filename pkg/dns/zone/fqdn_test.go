package zone

import (
	"strings"
	"testing"
)

func TestFQDN(t *testing.T) {
	label63 := strings.Repeat("a", 63)
	// 254 presentation bytes correspond to the maximum 255-byte wire name.
	maxName := strings.Repeat(label63+".", 3) + strings.Repeat("b", 61) + "."
	for _, tc := range []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{name: "root", input: ".", want: "."},
		{name: "single label", input: "Lantern.", want: "lantern."},
		{name: "absolute name", input: "pi.home.arpa.", want: "pi.home.arpa."},
		{name: "ASCII case folding", input: "PI.Home.ARPA.", want: "pi.home.arpa."},
		{name: "service labels", input: "_HTTPS._TCP.Home.ARPA.", want: "_https._tcp.home.arpa."},
		{name: "wildcard syntax", input: "*.Home.ARPA.", want: "*.home.arpa."},
		{name: "non ASCII bytes preserved", input: "ÄBC.Example.", want: "Äbc.example."},
		{name: "non UTF8 byte preserved", input: "\xffA.Example.", want: "\xffa.example."},
		{name: "maximum label", input: label63 + ".", want: label63 + "."},
		{name: "maximum name", input: maxName, want: maxName},
		{name: "empty", wantError: true},
		{name: "relative single label", input: "lantern", wantError: true},
		{name: "relative multiple labels", input: "pi.home.arpa", wantError: true},
		{name: "leading empty label", input: ".home.arpa.", wantError: true},
		{name: "interior empty label", input: "pi..home.arpa.", wantError: true},
		{name: "extra trailing dot", input: "home.arpa..", wantError: true},
		{name: "two root dots", input: "..", wantError: true},
		{name: "oversized label", input: strings.Repeat("a", 64) + ".", wantError: true},
		{name: "oversized name", input: strings.Repeat(label63+".", 3) + strings.Repeat("b", 62) + ".", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := FQDN(tc.input)
			if (err != nil) != tc.wantError {
				t.Fatalf("FQDN(%q): error=%v, wantError=%v", tc.input, err, tc.wantError)
			}
			if got != tc.want {
				t.Fatalf("FQDN(%q)=%q, want %q", tc.input, got, tc.want)
			}
			if err == nil {
				again, err := FQDN(got)
				if err != nil || again != got {
					t.Fatalf("normalization is not idempotent: got %q, error=%v", again, err)
				}
			}
		})
	}
}
