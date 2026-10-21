//go:build integration

package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This is intentionally secret-free: it builds inert .deb fixtures, uses a
// disposable key, and downloads packages through isolated APT clients. It never
// installs a package or contacts GitHub/the production APT repository.
func TestAPTRepositoryCLI(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux APT/GPG tools; no substitute for real client verification")
	}
	for _, program := range []string{"go", "gpg", "gpgconf", "apt-get", "apt-cache", "apt-ftparchive", "dpkg", "dpkg-deb"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Fatalf("Linux APT verification requires %s: %v", program, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	root := t.TempDir()
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "LANTERN_") && key != "APT_SIGNING_KEY" && key != "APT_SIGNING_PASSPHRASE" && key != "APT_CONFIG" && key != "GNUPGHOME" {
			env = append(env, entry)
		}
	}
	run := func(input []byte, extra []string, dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = append(append([]string{}, env...), extra...)
		cmd.Dir = dir
		cmd.Stdin = bytes.NewReader(input)
		cmd.WaitDelay = 2 * time.Second
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		out, err := cmd.Output()
		if err != nil {
			return out, fmt.Errorf("%s: %w\n%s", name, err, diagnostics.String())
		}
		return out, nil
	}
	must := func(input []byte, extra []string, dir, name string, args ...string) []byte {
		t.Helper()
		out, err := run(input, extra, dir, name, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	writeFile := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(root, "key")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, "gpgconf", "--homedir", home, "--kill", "gpg-agent").Run()
	})
	passphrase := []byte("disposable-apt-test-passphrase\n")
	must(passphrase, nil, "", "gpg", "--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0", "--quick-generate-key", "Lantern disposable test key", "ed25519", "sign", "1d")
	listing := must(nil, nil, "", "gpg", "--homedir", home, "--with-colons", "--list-keys")
	fingerprint := ""
	for _, line := range strings.Split(string(listing), "\n") {
		if fields := strings.Split(line, ":"); len(fields) > 9 && fields[0] == "fpr" {
			fingerprint = fields[9]
			break
		}
	}
	if fingerprint == "" {
		t.Fatal("no disposable key fingerprint")
	}
	secret := must(passphrase, nil, "", "gpg", "--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0", "--armor", "--export-secret-keys", fingerprint)
	signingEnv := []string{"APT_SIGNING_KEY=" + string(secret), "APT_SIGNING_PASSPHRASE=" + strings.TrimSuffix(string(passphrase), "\n")}
	program := filepath.Join(root, "aptrepo")
	must(nil, []string{"CGO_ENABLED=0", "GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH}, project, "go", "build", "-trimpath", "-ldflags=-X main.signingFingerprint="+fingerprint, "-o", program, "./cmd/aptrepo")

	type asset struct {
		Name string `json:"name"`
	}
	type release struct {
		Tag        string  `json:"tag_name"`
		Draft      bool    `json:"draft"`
		Prerelease bool    `json:"prerelease"`
		Assets     []asset `json:"assets"`
	}
	var releases []release
	fixtureRoot := filepath.Join(root, "releases")
	for _, version := range []string{"0.1.0", "0.1.5", "0.1.6"} {
		r := release{Tag: "v" + version, Draft: version == "0.1.6", Assets: []asset{{Name: "SHA256SUMS"}}}
		var checksums strings.Builder
		for _, arch := range []string{"amd64", "arm64", "armhf"} {
			if version == "0.1.0" && arch == "amd64" {
				continue
			}
			packageDir := filepath.Join(root, "package-"+version+"-"+arch)
			control := fmt.Sprintf("Package: lantern\nVersion: %s-1\nArchitecture: %s\nMaintainer: Fixture <fixture@example.invalid>\nDescription: inert APT fixture\n", version, arch)
			writeFile(filepath.Join(packageDir, "DEBIAN/control"), []byte(control), 0644)
			name := "lantern_" + version + "-1_" + arch + ".deb"
			path := filepath.Join(fixtureRoot, r.Tag, name)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			must(nil, nil, "", "dpkg-deb", "--build", "--root-owner-group", packageDir, path)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&checksums, "%x  ./%s\n", sha256.Sum256(data), name)
			r.Assets = append(r.Assets, asset{Name: name})
		}
		writeFile(filepath.Join(fixtureRoot, r.Tag, "SHA256SUMS"), []byte(checksums.String()), 0644)
		releases = append(releases, r)
	}
	releases = append(releases, release{Tag: "v1.0.0-rc.1", Prerelease: true})
	releasesPath := filepath.Join(root, "releases.json")
	updateReleases := func() {
		t.Helper()
		data, err := json.Marshal([][]release{releases})
		if err != nil {
			t.Fatal(err)
		}
		writeFile(releasesPath, data, 0644)
	}
	updateReleases()
	// The only mocked external program is GitHub CLI. Everything involving
	// Debian archives, GnuPG, indexes, signatures, HTTP, and APT is real.
	stub := `#!/bin/sh
set -eu
if [ "$1" = api ]; then
  cat "$APT_TEST_RELEASES"
elif [ "$1" = release ] && [ "$2" = download ]; then
  tag="$3"; shift 3
  dest=''
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --dir ]; then dest="$2"; fi
    shift
  done
  test -n "$dest"
  cp "$APT_TEST_FIXTURES/$tag/"* "$dest/"
else
  echo 'Unexpected GitHub invocation' >&2; exit 2
fi
`
	bin := filepath.Join(root, "bin")
	writeFile(filepath.Join(bin, "gh"), []byte(stub), 0755)
	env = append(env, "PATH="+bin+":"+os.Getenv("PATH"), "APT_TEST_RELEASES="+releasesPath, "APT_TEST_FIXTURES="+fixtureRoot)
	var mu sync.RWMutex
	previousSite := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		if previousSite == "" {
			http.NotFound(w, r)
			return
		}
		http.FileServer(http.Dir(previousSite)).ServeHTTP(w, r)
	}))
	defer server.Close()
	setPrevious := func(site string) { mu.Lock(); previousSite = site; mu.Unlock() }
	build := func(site string, bootstrap bool) error {
		args := []string{"build", "--site", site, "--repository", "test/lantern", "--url", server.URL}
		if bootstrap {
			args = append(args, "--bootstrap")
		}
		_, err := run(nil, nil, "", program, args...)
		return err
	}
	readCatalog := func(site string) map[string]json.RawMessage {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(site, "dists/stable/catalog.json"))
		if err != nil {
			t.Fatal(err)
		}
		var c map[string]json.RawMessage
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	packageCount := func(site string) int {
		t.Helper()
		var packages map[string]json.RawMessage
		if err := json.Unmarshal(readCatalog(site)["packages"], &packages); err != nil {
			t.Fatal(err)
		}
		return len(packages)
	}
	sign := func(site string) { must(nil, signingEnv, "", program, "sign", "--site", site) }
	smoke := func(site string) { must(nil, nil, "", program, "smoke", "--site", site) }

	// Cover the transition where new tooling precedes the first amd64 release.
	releases[1].Draft = true
	updateReleases()
	legacy := filepath.Join(root, "legacy")
	if err := build(legacy, true); err != nil {
		t.Fatal(err)
	}
	if packageCount(legacy) != 2 {
		t.Fatal("legacy ARM packages changed")
	}
	sign(legacy)
	smoke(legacy)
	releases[1].Draft = false
	updateReleases()
	site := filepath.Join(root, "site")
	if err := build(site, true); err != nil {
		t.Fatal(err)
	}
	if packageCount(site) != 5 {
		t.Fatal("mixed history should contain five packages")
	}
	wrongPass := []string{signingEnv[0], "APT_SIGNING_PASSPHRASE=wrong"}
	if _, err := run(nil, wrongPass, "", program, "sign", "--site", site); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	sign(site)
	smoke(site)
	// A normal production-pinned binary must reject the disposable key both
	// when signing and when APT authenticates the fixture repository.
	wrongKeyProgram := filepath.Join(root, "aptrepo-production-pin")
	must(nil, []string{"CGO_ENABLED=0", "GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH}, project, "go", "build", "-trimpath", "-o", wrongKeyProgram, "./cmd/aptrepo")
	if _, err := run(nil, signingEnv, "", wrongKeyProgram, "sign", "--site", site); err == nil {
		t.Fatal("wrong signing fingerprint accepted")
	}
	if _, err := run(nil, nil, "", wrongKeyProgram, "smoke", "--site", site); err == nil {
		t.Fatal("APT accepted wrong trusted fingerprint")
	}
	setPrevious(site)
	oldCatalog := readCatalog(site)
	releases[2].Draft = false
	updateReleases()
	second := filepath.Join(root, "second")
	if err := build(second, false); err != nil {
		t.Fatal(err)
	}
	if packageCount(second) != 8 {
		t.Fatal("second generation should contain eight packages")
	}
	var oldIndexes, newIndexes map[string]string
	if err := json.Unmarshal(oldCatalog["indexes"], &oldIndexes); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readCatalog(second)["indexes"], &newIndexes); err != nil {
		t.Fatal(err)
	}
	for name, sha := range oldIndexes {
		if newIndexes[name] != sha {
			t.Fatalf("historical index lost: %s", name)
		}
	}
	sign(second)
	smoke(second)
	if _, err := run(nil, nil, "", program, "smoke", "--site", second, "--url", server.URL); err == nil {
		t.Fatal("live smoke accepted an older candidate than staged metadata")
	}

	// Regression: extraction must not accept unsigned OpenPGP packets.
	inRelease := filepath.Join(site, "dists/stable/InRelease")
	savedSigned, err := os.ReadFile(inRelease)
	if err != nil {
		t.Fatal(err)
	}
	releaseData, err := os.ReadFile(filepath.Join(site, "dists/stable/Release"))
	if err != nil {
		t.Fatal(err)
	}
	unsigned := must(releaseData, nil, "", "gpg", "--homedir", home, "--batch", "--store")
	writeFile(inRelease, unsigned, 0644)
	if err := build(filepath.Join(root, "unsigned"), false); err == nil {
		t.Fatal("unsigned previous Release accepted")
	}
	writeFile(inRelease, savedSigned, 0644)
	writeFile(inRelease, bytes.Replace(savedSigned, []byte("Origin: Lantern"), []byte("Origin: altered"), 1), 0644)
	if _, err := run(nil, nil, "", program, "smoke", "--site", site); err == nil {
		t.Fatal("APT accepted corrupt signed metadata")
	}
	writeFile(inRelease, savedSigned, 0644)
	manifest := filepath.Join(site, "dists/stable/catalog.json")
	manifestData, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(manifest, append(manifestData, ' '), 0644)
	if err := build(filepath.Join(root, "bad-catalog"), false); err == nil {
		t.Fatal("modified previous catalog accepted")
	}
	writeFile(manifest, manifestData, 0644)
	writeFile(filepath.Join(site, "pool/main/l/lantern/lantern_0.1.5-1_amd64.deb"), []byte("corrupt"), 0644)
	if _, err := run(nil, nil, "", program, "smoke", "--site", site); err == nil {
		t.Fatal("changed package accepted")
	}

	// Optional one-off migration parity test. Python is not a dependency of the
	// Go tool or normal tests. Supply the historical script explicitly on Linux.
	if baseline := os.Getenv("LANTERN_APT_PYTHON_BASELINE"); baseline != "" {
		baseline, err = filepath.Abs(baseline)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := exec.LookPath("python3"); err != nil {
			t.Fatal("optional differential test requires Python")
		}
		setPrevious("")
		releases[2].Draft = true
		updateReleases()
		pySite := filepath.Join(root, "python")
		goSite := filepath.Join(root, "go-parity")
		const entry = `import importlib.machinery,types,sys; p,f=sys.argv[1:3]; m=types.ModuleType("baseline"); importlib.machinery.SourceFileLoader("baseline",p).exec_module(m); m.FINGERPRINT=f; sys.argv=[p]+sys.argv[3:]; m.main()`
		python := func(extra []string, args ...string) {
			t.Helper()
			must(nil, extra, "", "python3", append([]string{"-c", entry, baseline, fingerprint}, args...)...)
		}
		python(nil, "build", "--site", pySite, "--repository", "test/lantern", "--url", server.URL, "--bootstrap")
		if err := build(goSite, true); err != nil {
			t.Fatal(err)
		}
		var pyPackages, goPackages map[string]json.RawMessage
		if err := json.Unmarshal(readCatalog(pySite)["packages"], &pyPackages); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(readCatalog(goSite)["packages"], &goPackages); err != nil {
			t.Fatal(err)
		}
		// Compare JSON semantically: field order and whitespace are not a format contract.
		var pyDecoded, goDecoded any
		pyData, _ := json.Marshal(pyPackages)
		goData, _ := json.Marshal(goPackages)
		if err := json.Unmarshal(pyData, &pyDecoded); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(goData, &goDecoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pyDecoded, goDecoded) {
			t.Fatal("Python/Go package catalog mismatch")
		}
		for _, arch := range []string{"amd64", "arm64", "armhf"} {
			var indexes [][]byte
			for _, path := range []string{pySite, goSite} {
				data, err := os.ReadFile(filepath.Join(path, "dists/stable/main/binary-"+arch+"/Packages.gz"))
				if err != nil {
					t.Fatal(err)
				}
				gz, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				plain, err := io.ReadAll(gz)
				gz.Close()
				if err != nil {
					t.Fatal(err)
				}
				indexes = append(indexes, plain)
			}
			if !bytes.Equal(indexes[0], indexes[1]) {
				t.Fatalf("Python/Go %s index mismatch", arch)
			}
		}
		// Cross-sign and consume each other's artifacts.
		sign(pySite)
		python(signingEnv, "sign", "--site", goSite)
		smoke(pySite)
		python(nil, "smoke", "--site", goSite)
		setPrevious(pySite)
		releases[2].Draft = false
		updateReleases()
		if err := build(filepath.Join(root, "go-after-python"), false); err != nil {
			t.Fatal(err)
		}
		setPrevious(goSite)
		python(nil, "build", "--site", filepath.Join(root, "python-after-go"), "--repository", "test/lantern", "--url", server.URL)
		t.Log("Python/Go package, index, signature, APT-client, and historical-repository parity passed")
	}
	t.Log("Go APT lifecycle and negative checks passed; no packages installed")
}
