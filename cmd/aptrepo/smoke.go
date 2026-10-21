package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (t *tool) smoke(ctx context.Context, site, url string) error {
	c, err := verifySite(site)
	if err != nil {
		return err
	}
	if url == "" {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		server := &http.Server{Handler: http.FileServer(http.Dir(site)), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = server.Serve(listener)
		}()
		defer func() { _ = server.Close(); <-done }()
		url = "http://" + listener.Addr().String()
	}
	for _, arch := range architectures {
		if err := t.smokeArchitecture(ctx, site, url, arch, c); err != nil {
			return fmt.Errorf("%s smoke: %w", arch, err)
		}
	}
	return nil
}

func (t *tool) smokeArchitecture(ctx context.Context, site, url, arch string, c catalog) error {
	root, err := os.MkdirTemp("", "apt-client-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	for _, dir := range []string{"empty", "state/lists/partial", "cache/archives/partial", "log", "downloads"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0755); err != nil {
			return err
		}
	}
	if err := write(filepath.Join(root, "status"), nil); err != nil {
		return err
	}
	key, err := readLimited(filepath.Join(site, "lantern-archive-keyring.asc"), metadataLimit)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(root, "lantern.asc")
	if err := write(keyPath, key); err != nil {
		return err
	}
	sources := fmt.Sprintf("Types: deb\nURIs: %s\nSuites: stable\nComponents: main\nArchitectures: %s\nSigned-By: %s %s\nBy-Hash: force\n", url, arch, keyPath, t.fingerprint)
	if err := write(filepath.Join(root, "lantern.sources"), []byte(sources)); err != nil {
		return err
	}
	config := aptConfig(root, arch)
	configPath := filepath.Join(root, "apt.conf")
	if err := write(configPath, []byte(config)); err != nil {
		return err
	}
	env := childEnvironment(nil)
	env = append(env, "APT_CONFIG="+configPath, "LC_ALL=C")
	if _, err := t.run(ctx, command{name: "apt-get", args: []string{"--error-on=any", "update"}, env: env}); err != nil {
		return err
	}
	var names []string
	for name, entry := range c.Packages {
		if entry.Architecture == arch {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		fmt.Printf("No published %s packages yet; index verification passed\n", arch)
		return nil
	}
	sort.Strings(names)
	latest := c.Packages[names[0]].Version
	for _, name := range names[1:] {
		candidate := c.Packages[name].Version
		greater, err := t.versionLess(ctx, latest, candidate)
		if err != nil {
			return err
		}
		if greater {
			latest = candidate
		}
	}
	policy, err := t.run(ctx, command{name: "apt-cache", args: []string{"policy", "lantern:" + arch}, env: env})
	if err != nil {
		return err
	}
	found := false
	for _, line := range strings.Split(string(policy), "\n") {
		if strings.TrimSpace(line) == "Candidate: "+latest {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("unexpected APT candidate: %s", policy)
	}
	for _, name := range names {
		entry := c.Packages[name]
		if _, err := t.run(ctx, command{name: "apt-get", args: []string{"download", "lantern:" + arch + "=" + entry.Version}, env: env, dir: filepath.Join(root, "downloads")}); err != nil {
			return err
		}
		data, err := readLimited(filepath.Join(root, "downloads", filepath.Base(name)), packageLimit)
		if err != nil {
			return err
		}
		if digest(data) != entry.SHA256 {
			return fmt.Errorf("APT downloaded unexpected package bytes: %s", name)
		}
	}
	fmt.Printf("%s: candidate %s; verified %d package downloads\n", arch, latest, len(names))
	return nil
}

func aptConfig(root, arch string) string {
	settings := [][2]string{
		{"Dir::Etc::main", "/dev/null"}, {"Dir::Etc::parts", filepath.Join(root, "empty")},
		{"Dir::Etc::sourcelist", filepath.Join(root, "lantern.sources")}, {"Dir::Etc::sourceparts", filepath.Join(root, "empty")},
		{"Dir::Etc::preferences", "/dev/null"}, {"Dir::Etc::preferencesparts", filepath.Join(root, "empty")},
		{"Dir::Etc::trusted", "/dev/null"}, {"Dir::Etc::trustedparts", filepath.Join(root, "empty")},
		{"Dir::State", filepath.Join(root, "state")}, {"Dir::State::status", filepath.Join(root, "status")},
		{"Dir::Cache", filepath.Join(root, "cache")}, {"Dir::Cache::pkgcache", ""}, {"Dir::Cache::srcpkgcache", ""},
		{"Dir::Log", filepath.Join(root, "log")}, {"APT::Architecture", arch}, {"Acquire::Languages", "none"},
	}
	var output strings.Builder
	for _, pair := range settings {
		fmt.Fprintf(&output, "%s %q;\n", pair[0], pair[1])
	}
	fmt.Fprintf(&output, "#clear APT::Architectures;\nAPT::Architectures { %q; };\n", arch)
	return output.String()
}

func (t *tool) versionLess(ctx context.Context, left, right string) (bool, error) {
	if left == right {
		return false, nil
	}
	_, err := t.command(ctx, "dpkg", "--compare-versions", left, "lt", right)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
