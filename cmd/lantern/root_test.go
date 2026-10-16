package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			ctx := t.Context()
			cmd := newRootCommand(func(gotCtx context.Context, address, webAddress string) error {
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
	want := errors.New("listen failed")
	cmd := newRootCommand(func(context.Context, string, string) error { return want })
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
