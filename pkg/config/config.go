// Package config loads and validates Lantern's startup settings. It does not
// manage DNS records or persist edits made through the web UI.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DNS             DNS           `mapstructure:"dns"`
	Web             Web           `mapstructure:"web"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type DNS struct {
	Address        string        `mapstructure:"address"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	HandlerTimeout time.Duration `mapstructure:"handler_timeout"`
	Concurrency    int           `mapstructure:"concurrency"`
}

type Web struct {
	Address           string        `mapstructure:"address"`
	AllowedHosts      []string      `mapstructure:"allowed_hosts"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
	MaxHeaderBytes    int           `mapstructure:"max_header_bytes"`
}

// Defaults preserves the application's development defaults, not the lower-level
// DNS transport's independent defaults. Listen addresses stay loopback-only.
func Defaults() Config {
	return Config{
		DNS:             DNS{Address: "127.0.0.1:1053", ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, HandlerTimeout: 10 * time.Second, Concurrency: 10},
		Web:             Web{Address: "127.0.0.1:8080", AllowedHosts: []string{}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10},
		ShutdownTimeout: 5 * time.Second,
	}
}

// Validate rejects invalid startup settings without resolving hostnames or
// opening sockets. Application timeouts must be positive; zero does not disable them.
func (c Config) Validate() error {
	for _, field := range []struct{ name, address string }{{"dns.address", c.DNS.Address}, {"web.address", c.Web.Address}} {
		host, port, err := net.SplitHostPort(field.address)
		if err != nil {
			return fmt.Errorf("%s must be a host:port address: %w", field.name, err)
		}
		if strings.ContainsAny(host, "/\\@ \t\r\n\x00") {
			return fmt.Errorf("%s contains an invalid listen host", field.name)
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return fmt.Errorf("%s must contain a numeric port between 0 and 65535", field.name)
		}
	}
	for _, field := range []struct {
		name  string
		value time.Duration
	}{
		{"dns.read_timeout", c.DNS.ReadTimeout}, {"dns.write_timeout", c.DNS.WriteTimeout}, {"dns.handler_timeout", c.DNS.HandlerTimeout},
		{"web.read_header_timeout", c.Web.ReadHeaderTimeout}, {"web.read_timeout", c.Web.ReadTimeout}, {"web.write_timeout", c.Web.WriteTimeout}, {"web.idle_timeout", c.Web.IdleTimeout},
		{"shutdown_timeout", c.ShutdownTimeout},
	} {
		if field.value <= 0 {
			return fmt.Errorf("%s must be greater than zero", field.name)
		}
	}
	if c.DNS.Concurrency <= 0 {
		return fmt.Errorf("dns.concurrency must be greater than zero")
	}
	if c.Web.MaxHeaderBytes <= 0 {
		return fmt.Errorf("web.max_header_bytes must be greater than zero")
	}
	for _, host := range c.Web.AllowedHosts {
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.Unmap().IsUnspecified() || ip.Zone() != "" {
				return fmt.Errorf("web.allowed_hosts: %q must be a concrete, unscoped IP", host)
			}
			continue
		}
		if host == "" || strings.ContainsAny(host, ":/\\@*[] \t\r\n") {
			return fmt.Errorf("web.allowed_hosts: %q must be a hostname or IP without a scheme, port, or wildcard", host)
		}
		name := strings.TrimSuffix(host, ".")
		if name == "" || len(name) > 253 {
			return fmt.Errorf("web.allowed_hosts: %q is not a valid hostname", host)
		}
		for _, label := range strings.Split(name, ".") {
			if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return fmt.Errorf("web.allowed_hosts: %q is not a valid hostname", host)
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
					return fmt.Errorf("web.allowed_hosts: %q is not a valid hostname", host)
				}
			}
		}
	}
	return nil
}
