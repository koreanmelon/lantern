package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "LANTERN_") {
			t.Setenv(key, "") // Register restoration before unsetting it for the test.
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func configFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lantern.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func flags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	set := pflag.NewFlagSet("test", pflag.ContinueOnError)
	set.String("dns-listen", Defaults().DNS.Address, "")
	set.String("web-listen", Defaults().Web.Address, "")
	if err := set.Parse(args); err != nil {
		t.Fatal(err)
	}
	return set
}

func TestLoadPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     bool
		env      bool
		args     []string
		dns, web string
	}{
		{"defaults", false, false, nil, "127.0.0.1:1053", "127.0.0.1:8080"},
		{"file beats unchanged flags", true, false, nil, "127.0.0.1:2053", "127.0.0.1:8081"},
		{"environment beats file", true, true, nil, "127.0.0.1:3053", "127.0.0.1:8082"},
		{"explicit default beats environment", true, true, []string{"--dns-listen", "127.0.0.1:1053"}, "127.0.0.1:1053", "127.0.0.1:8082"},
		{"flags beat file", true, false, []string{"--web-listen", "127.0.0.1:8083"}, "127.0.0.1:2053", "127.0.0.1:8083"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			path := ""
			if tc.file {
				path = configFile(t, "dns:\n  address: 127.0.0.1:2053\nweb:\n  address: 127.0.0.1:8081\n")
			}
			if tc.env {
				t.Setenv("LANTERN_DNS_ADDRESS", "127.0.0.1:3053")
				t.Setenv("LANTERN_WEB_ADDRESS", "127.0.0.1:8082")
			}
			cfg, err := Load(path, flags(t, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DNS.Address != tc.dns || cfg.Web.Address != tc.web {
				t.Fatalf("unexpected addresses: %+v", cfg)
			}
			if cfg.DNS.ReadTimeout != 10*time.Second || cfg.DNS.Concurrency != 10 {
				t.Fatal("defaults changed")
			}
		})
	}
}

func TestLoadTypedEnvironmentAndFile(t *testing.T) {
	cleanEnv(t)
	path := configFile(t, "dns:\n  write_timeout: 4s\nweb:\n  allowed_hosts: [file.example]\n  max_header_bytes: 4096\nshutdown_timeout: 8s\n")
	t.Setenv("LANTERN_DNS_READ_TIMEOUT", "2s")
	t.Setenv("LANTERN_RECORDS_FILE", "/var/lib/lantern/test-records.json")
	t.Setenv("LANTERN_DNS_HANDLER_TIMEOUT", "3s")
	t.Setenv("LANTERN_DNS_CONCURRENCY", "17")
	t.Setenv("LANTERN_WEB_ALLOWED_HOSTS", "pi.home.arpa, lantern.example.ts.net")
	t.Setenv("LANTERN_WEB_IDLE_TIMEOUT", "2m")
	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DNS.ReadTimeout != 2*time.Second || cfg.DNS.WriteTimeout != 4*time.Second || cfg.DNS.HandlerTimeout != 3*time.Second || cfg.DNS.Concurrency != 17 || cfg.Web.MaxHeaderBytes != 4096 || cfg.Web.IdleTimeout != 2*time.Minute || cfg.ShutdownTimeout != 8*time.Second {
		t.Fatalf("typed settings were not loaded: %+v", cfg)
	}
	if cfg.RecordsFile != "/var/lib/lantern/test-records.json" {
		t.Fatal("record file environment override was not loaded")
	}
	if !reflect.DeepEqual(cfg.Web.AllowedHosts, []string{"pi.home.arpa", "lantern.example.ts.net"}) {
		t.Fatalf("hosts: %v", cfg.Web.AllowedHosts)
	}
	t.Setenv("LANTERN_WEB_ALLOWED_HOSTS", "")
	cfg, err = Load(path, nil)
	if err != nil || len(cfg.Web.AllowedHosts) != 0 {
		t.Fatalf("explicit empty hosts did not clear file value: %v", err)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"malformed YAML", "dns: ["},
		{"empty record path", "records_file: ''\n"},
		{"unknown key", "dns:\n  concurency: 12\n"},
		{"invalid address", "dns:\n  address: not-an-address\n"},
		{"address whitespace", "web:\n  address: 'bad host:8080'\n"},
		{"port out of range", "web:\n  address: localhost:65536\n"},
		{"named port", "dns:\n  address: localhost:domain\n"},
		{"negative timeout", "dns:\n  read_timeout: -1s\n"},
		{"zero timeout", "web:\n  read_timeout: 0s\n"},
		{"duration without units", "dns:\n  read_timeout: 10\n"},
		{"invalid duration", "shutdown_timeout: someday\n"},
		{"zero concurrency", "dns:\n  concurrency: 0\n"},
		{"fractional concurrency", "dns:\n  concurrency: 1.5\n"},
		{"boolean concurrency", "dns:\n  concurrency: true\n"},
		{"negative headers", "web:\n  max_header_bytes: -1\n"},
		{"wildcard host", "web:\n  allowed_hosts: ['*.home.arpa']\n"},
		{"unspecified host", "web:\n  allowed_hosts: ['0.0.0.0']\n"},
		{"URL as host", "web:\n  allowed_hosts: ['http://pi.home.arpa']\n"},
		{"port in host", "web:\n  allowed_hosts: ['pi.home.arpa:8080']\n"},
		{"empty hostname", "web:\n  allowed_hosts: ['.']\n"},
		{"empty label", "web:\n  allowed_hosts: ['a..example']\n"},
		{"host control character", "web:\n  allowed_hosts: [\"evil\\u0000example\"]\n"},
		{"integer overflow", "dns:\n  concurrency: '18446744073709551616'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			if _, err := Load(configFile(t, tc.content), nil); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Run("empty environment address", func(t *testing.T) {
		cleanEnv(t)
		t.Setenv("LANTERN_DNS_ADDRESS", "")
		if _, err := Load("", nil); err == nil {
			t.Fatal("explicit empty address was ignored")
		}
	})
	t.Run("bad environment integer", func(t *testing.T) {
		cleanEnv(t)
		t.Setenv("LANTERN_DNS_CONCURRENCY", "lots")
		if _, err := Load("", nil); err == nil {
			t.Fatal("bad integer accepted")
		}
	})
}

func TestLoadExplicitPathsAndIsolation(t *testing.T) {
	cleanEnv(t)
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), nil); err == nil {
		t.Fatal("requested missing file ignored")
	}
	path := configFile(t, "dns:\n  address: 127.0.0.1:2053\n")
	t.Chdir(filepath.Dir(path))
	cfg, err := Load("", nil)
	if err != nil || cfg.DNS.Address != Defaults().DNS.Address {
		t.Fatalf("working-directory file was auto-loaded: %v", err)
	}
	cfg, err = Load(path, nil)
	if err != nil || cfg.DNS.Address != "127.0.0.1:2053" {
		t.Fatalf("explicit file not loaded: %v", err)
	}
	cfg, err = Load("", nil)
	if err != nil || cfg.DNS.Address != Defaults().DNS.Address {
		t.Fatalf("Viper state leaked between loads: %v", err)
	}
}

func TestLoadFileFormats(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"lantern.json", `{"dns":{"concurrency":12},"web":{"max_header_bytes":4096},"shutdown_timeout":"8s"}`},
		{"lantern.toml", "shutdown_timeout = '8s'\n[dns]\nconcurrency = 12\n[web]\nmax_header_bytes = 4096\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DNS.Concurrency != 12 || cfg.Web.MaxHeaderBytes != 4096 || cfg.ShutdownTimeout != 8*time.Second {
				t.Fatalf("incorrect typed values: %+v", cfg)
			}
		})
	}
}

func TestExampleConfigurationMatchesDefaults(t *testing.T) {
	cleanEnv(t)
	cfg, err := Load(filepath.Join("..", "..", "configs", "lantern.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	// Empty/nil lists are equivalent; all scalar defaults should stay in sync.
	cfg.Web.AllowedHosts, want.Web.AllowedHosts = nil, nil
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("sample and defaults diverged:\n got %+v\nwant %+v", cfg, want)
	}
}

func TestStrictNumericFormats(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"fractional.json", `{"dns":{"concurrency":1.5}}`},
		{"fractional.toml", "[dns]\nconcurrency = 1.5\n"},
		{"duration.json", `{"dns":{"read_timeout":10}}`},
		{"duration.toml", "[dns]\nread_timeout = 10\n"},
		{"overflow.json", `{"web":{"max_header_bytes":1e100}}`},
		{"overflow.toml", "[web]\nmax_header_bytes = '18446744073709551616'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path, nil); err == nil {
				t.Fatal("invalid numeric setting accepted")
			}
		})
	}
}
