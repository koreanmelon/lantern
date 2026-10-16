package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koreanmelon/lantern/internal/config"
)

func TestRootCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		address    string
		webAddress string
		wantError  bool
	}{
		{name: "defaults", address: "127.0.0.1:1053"},
		{name: "custom address", args: []string{"--dns-listen", "127.0.0.1:2053"}, address: "127.0.0.1:2053"},
		{name: "custom web address", args: []string{"--web-listen", "127.0.0.1:8081"}, address: "127.0.0.1:1053", webAddress: "127.0.0.1:8081"},
		{name: "positional argument", args: []string{"unexpected"}, wantError: true},
		{name: "unknown flag", args: []string{"--unknown"}, wantError: true},
		{name: "missing flag value", args: []string{"--dns-listen"}, wantError: true},
		{name: "help", args: []string{"--help"}},
		{name: "help skips config loading", args: []string{"--config", "/does/not/exist.yaml", "--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanConfigEnv(t)
			called := false
			ctx := t.Context()
			cmd := newRootCommand(func(gotCtx context.Context, cfg config.Config) error {
				address, webAddress := cfg.DNS.Address, cfg.Web.Address
				called = true
				wantWeb := tc.webAddress
				if wantWeb == "" {
					wantWeb = "127.0.0.1:8080"
				}
				if webAddress != wantWeb {
					t.Fatalf("got web address %q, want %q", webAddress, wantWeb)
				}
				if gotCtx != ctx || address != tc.address {
					t.Fatalf("unexpected invocation: context=%v address=%q", gotCtx, address)
				}
				return nil
			})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(ctx)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if called != (tc.address != "") {
				t.Fatalf("unexpected serve invocation: %v", called)
			}
			if tc.name == "help" && !strings.Contains(output.String(), "--dns-listen") {
				t.Fatal("help does not document DNS flag")
			}
		})
	}
}

func TestRootCommandReturnsServerError(t *testing.T) {
	cleanConfigEnv(t)
	want := errors.New("listen failed")
	cmd := newRootCommand(func(context.Context, config.Config) error { return want })
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(t.Context()); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if strings.Contains(output.String(), "Usage:") || strings.Contains(output.String(), "Error:") {
		t.Fatal("Cobra printed duplicate error or usage output")
	}
}

func cleanConfigEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "LANTERN_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRootCommandLoadsConfiguration(t *testing.T) {
	cleanConfigEnv(t)
	path := filepath.Join(t.TempDir(), "lantern.yaml")
	if err := os.WriteFile(path, []byte("dns:\n  address: 127.0.0.1:2053\n  concurrency: 23\nweb:\n  address: 127.0.0.1:8081\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_WEB_ADDRESS", "127.0.0.1:8082")
	called := false
	cmd := newRootCommand(func(_ context.Context, cfg config.Config) error {
		called = true
		if cfg.DNS.Address != "127.0.0.1:3053" || cfg.DNS.Concurrency != 23 || cfg.Web.Address != "127.0.0.1:8082" {
			t.Fatalf("incorrect resolved settings: %+v", cfg)
		}
		return nil
	})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config", path, "--dns-listen", "127.0.0.1:3053"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !called || !strings.Contains(output.String(), "127.0.0.1:8082") {
		t.Fatal("startup did not use/log effective configuration")
	}
}

func TestRootCommandDoesNotStartWithBadConfiguration(t *testing.T) {
	cleanConfigEnv(t)
	called := false
	cmd := newRootCommand(func(context.Context, config.Config) error { called = true; return nil })
	cmd.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")})
	if err := cmd.ExecuteContext(t.Context()); err == nil || called {
		t.Fatal("bad configuration did not prevent startup")
	}
}
