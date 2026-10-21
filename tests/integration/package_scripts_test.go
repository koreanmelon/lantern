//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDebianReloadScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	for _, hook := range []string{"postinst", "postrm"} {
		t.Run(hook, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "build", "package", "scripts", hook))
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			normalAction := "configure"
			if hook == "postrm" {
				normalAction = "remove"
			}
			const guard = "[ -d /run/systemd/system ]"
			if strings.Count(source, guard) != 1 {
				t.Fatal("expected one systemd directory guard for safe test substitution")
			}
			for _, tc := range []struct {
				name, action                     string
				systemd, binary, failure, reload bool
			}{
				{"fresh configure", "configure", true, true, false, hook == "postinst"},
				{"remove", "remove", true, true, false, hook == "postrm"},
				{"purge", "purge", true, true, false, hook == "postrm"},
				{"upgrade old postrm", "upgrade", true, true, false, false},
				{"failed upgrade", "failed-upgrade", true, true, false, false},
				{"abort upgrade", "abort-upgrade", true, true, false, false},
				{"abort install", "abort-install", true, true, false, false},
				{"abort remove", "abort-remove", true, true, false, false},
				{"disappear", "disappear", true, true, false, false},
				{"missing action", "", true, true, false, false},
				{"non systemd", normalAction, false, true, false, false},
				{"missing command", normalAction, true, false, false, false},
				{"reload failure configure", "configure", true, true, true, hook == "postinst"},
				{"reload failure remove", "remove", true, true, true, hook == "postrm"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					root := t.TempDir()
					bin := filepath.Join(root, "bin")
					if err := os.Mkdir(bin, 0755); err != nil {
						t.Fatal(err)
					}
					log := filepath.Join(root, "calls")
					state := filepath.Join(root, "records.json")
					if err := os.WriteFile(state, []byte("private record state"), 0600); err != nil {
						t.Fatal(err)
					}
					if tc.binary {
						stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOOK_LOG\"\nexit \"$HOOK_EXIT\"\n"
						if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(stub), 0755); err != nil {
							t.Fatal(err)
						}
					}
					probe := filepath.Join(root, "systemd")
					if tc.systemd {
						if err := os.Mkdir(probe, 0755); err != nil {
							t.Fatal(err)
						}
					}
					// Change only the guard path in memory; packaged script bytes
					// are checked separately. PATH cannot reach the host systemctl.
					script := strings.Replace(source, guard, "[ -d '"+probe+"' ]", 1)
					exit := "0"
					if tc.failure {
						exit = "7"
					}
					for attempt := 1; attempt <= 2; attempt++ {
						ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						args := []string{"-s", "--", tc.action}
						if tc.action == "configure" && attempt == 2 {
							args = append(args, "0.1.5-1")
						}
						cmd := exec.CommandContext(ctx, "/bin/sh", args...)
						cmd.Env = []string{"PATH=" + bin, "HOOK_LOG=" + log, "HOOK_EXIT=" + exit}
						cmd.Stdin = strings.NewReader(script)
						out, err := cmd.CombinedOutput()
						cancel()
						if err != nil {
							t.Fatalf("hook failed: %v\n%s", err, out)
						}
						if tc.reload && tc.failure && !strings.Contains(string(out), "warning: systemd daemon-reload failed") {
							t.Fatal("reload failure was hidden")
						}
						calls, err := os.ReadFile(log)
						if tc.reload {
							if err != nil || string(calls) != strings.Repeat("--system daemon-reload\n", attempt) {
								t.Fatalf("unexpected service actions: %q %v", calls, err)
							}
						} else if !os.IsNotExist(err) {
							t.Fatalf("unexpected service action: %q %v", calls, err)
						}
						if data, err := os.ReadFile(state); err != nil || string(data) != "private record state" {
							t.Fatal("record state changed")
						}
					}
				})
			}
		})
	}
}
