package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func testRelease(tag string, archs ...string) release {
	r := release{Tag: tag, Assets: []releaseAsset{{Name: "SHA256SUMS"}}}
	for _, arch := range archs {
		r.Assets = append(r.Assets, releaseAsset{Name: "lantern_" + tag[1:] + "-1_" + arch + ".deb"})
	}
	return r
}

func TestReleaseSelection(t *testing.T) {
	legacy := testRelease("v0.1.2", "arm64", "armhf")
	modern := testRelease("v0.1.5", architectures...)
	for _, r := range []release{legacy, modern} {
		selected, err := releasePackages(r)
		if err != nil || len(selected) != len(r.Assets)-1 {
			t.Fatalf("%s: %v %v", r.Tag, selected, err)
		}
	}
	for _, missing := range architectures {
		r := modern
		r.Assets = slices.DeleteFunc(slices.Clone(r.Assets), func(a releaseAsset) bool { return strings.HasSuffix(a.Name, "_"+missing+".deb") })
		if _, err := releasePackages(r); err == nil {
			t.Errorf("accepted missing %s", missing)
		}
	}
	for _, mutate := range []func(*release){
		func(r *release) { r.Assets = append(r.Assets, r.Assets[1]) },
		func(r *release) { r.Assets = r.Assets[1:] },
		func(r *release) { r.Assets[1].Name = strings.Replace(r.Assets[1].Name, "-1_", "-2_", 1) },
		func(r *release) { r.Tag = "v01.1.5" },
	} {
		r := modern
		r.Assets = slices.Clone(r.Assets)
		mutate(&r)
		if _, err := releasePackages(r); err == nil {
			t.Errorf("accepted malformed release: %+v", r)
		}
	}
	old := testRelease("v0.1.4", architectures...)
	if _, err := releasePackages(old); err != nil {
		t.Fatal("legacy tag may also include amd64:", err)
	}
}

func TestPublishedReleases(t *testing.T) {
	draft := testRelease("v0.1.6", architectures...)
	draft.Draft = true
	pre := release{Tag: "v0.2.0-rc.1", Prerelease: true}
	input, _ := json.Marshal([][]release{{testRelease("v0.10.0"), draft}, {pre, testRelease("v0.2.0")}})
	got, err := publishedReleases(input)
	if err != nil || len(got) != 2 || got[0].Tag != "v0.2.0" || got[1].Tag != "v0.10.0" {
		t.Fatalf("selection/sorting: %v %v", got, err)
	}
	for _, data := range []string{`[]`, `[[{"tag_name":"v0.1.5"}],[{"tag_name":"v0.1.5"}]]`, `{}`} {
		if _, err := publishedReleases([]byte(data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

func TestChecksums(t *testing.T) {
	sha := strings.Repeat("a", 64)
	for _, prefix := range []string{"", "./"} {
		got, err := checksums([]byte(sha+"  "+prefix+"a.deb\n"), []string{"a.deb"})
		if err != nil || got["a.deb"] != sha {
			t.Fatalf("valid manifest: %v %v", got, err)
		}
	}
	for _, text := range []string{"bad", sha + "  ../a.deb\n", sha + "  a.deb\n" + sha + "  a.deb\n", sha + "  b.deb\n", sha + "  /a.deb\n"} {
		if _, err := checksums([]byte(text), []string{"a.deb"}); err == nil {
			t.Errorf("accepted %s", text)
		}
	}
}

func TestCatalogRetentionAndPaths(t *testing.T) {
	c := emptyCatalog()
	name := "pool/main/l/lantern/lantern_0.1.0-1_arm64.deb"
	entry := packageEntry{SHA256: strings.Repeat("a", 64), Size: 10, Version: "0.1.0-1", Architecture: "arm64"}
	c.Packages[name] = entry
	data, _ := json.Marshal(c)
	decoded, err := decodeCatalog(data)
	if err != nil || !reflect.DeepEqual(c, decoded) {
		t.Fatalf("round trip: %v %v", decoded, err)
	}
	if err := checkRetention(c, decoded); err != nil {
		t.Fatal(err)
	}
	delete(decoded.Packages, name)
	if checkRetention(c, decoded) == nil {
		t.Fatal("accepted deleted package")
	}
	changed := entry
	changed.SHA256 = strings.Repeat("b", 64)
	decoded.Packages[name] = changed
	if checkRetention(c, decoded) == nil {
		t.Fatal("accepted replaced package")
	}
	for _, bad := range []string{`null`, `{}`, `{"packages":null,"indexes":{}}`, string(data) + " {}", strings.Replace(string(data), "pool/main/l/lantern/", "../../escape/", 1), strings.Replace(string(data), `"size":10`, `"size":null`, 1), strings.Replace(string(data), `"packages":`, `"unknown":`, 1)} {
		if _, err := decodeCatalog([]byte(bad)); err == nil {
			t.Errorf("accepted bad catalog %s", bad)
		}
	}
	for _, bad := range []string{
		`{"packages":{},"indexes":{},"PACKAGES":{}}`,
		`{"packages":{},"indexes":{},"packages":null}`,
		`{"PACKAGES":{},"indexes":{}}`,
		strings.Replace(string(data), `"size":10`, `"size":10,"size":null`, 1),
		strings.Replace(string(data), `"size":10`, `"Size":10`, 1),
		strings.Replace(string(data), `"size":10,`, ``, 1),
	} {
		if _, err := decodeCatalog([]byte(bad)); err == nil {
			t.Errorf("accepted duplicate, alias, or missing field: %s", bad)
		}
	}
}

func TestByHashAndReleaseTable(t *testing.T) {
	data := []byte("Packages fixture")
	for _, algorithm := range []string{"SHA256", "SHA512"} {
		name := "dists/stable/main/binary-arm64/by-hash/" + algorithm + "/" + indexDigest(algorithm, data)
		if err := verifyIndex(name, data, digest(data)); err != nil {
			t.Fatal(err)
		}
		if verifyIndex(name, []byte("tampered"), digest(data)) == nil {
			t.Fatal("accepted changed index")
		}
	}
	line := fmt.Sprintf(" %s %d catalog.json\n", digest(data), len(data))
	table, err := releaseHashes([]byte("Origin: Lantern\nSHA256:\n" + line + "SHA512:\n ignored\n"))
	if err != nil || table["catalog.json"] != (hashEntry{sha: digest(data), size: int64(len(data))}) {
		t.Fatalf("hash table: %v %v", table, err)
	}
	for _, text := range []string{"SHA256:\n" + line + line, "SHA256:\n bad\n", "SHA256:\n " + digest(data) + " -1 catalog.json\n"} {
		if _, err := releaseHashes([]byte(text)); err == nil {
			t.Fatal("accepted invalid hash table")
		}
	}
}

func TestPinnedFingerprint(t *testing.T) {
	valid := "pub:u:3072:1:key:1:2:::::sc:\nfpr:::::::::" + signingFingerprint + ":\n"
	if err := checkFingerprint([]byte(valid), signingFingerprint); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", valid + valid, strings.Replace(valid, signingFingerprint, strings.Repeat("A", 40), 1), strings.Replace(valid, "pub:u:", "pub:e:", 1), strings.Replace(valid, "pub:u:", "pub:r:", 1)} {
		if err := checkFingerprint([]byte(text), signingFingerprint); err == nil {
			t.Fatalf("accepted invalid key listing %q", text)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(code int, data []byte) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}
}

func TestFetchAndBootstrap(t *testing.T) {
	for _, tc := range []struct {
		status    int
		bootstrap bool
		allowed   bool
	}{{404, true, true}, {404, false, false}, {503, true, false}, {403, true, false}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			tool := newTool()
			tool.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.status, nil), nil })
			_, err := tool.previousRepository(t.Context(), "https://example.invalid", t.TempDir(), tc.bootstrap)
			if (err == nil) != tc.allowed {
				t.Fatalf("bootstrap result: %v", err)
			}
		})
	}
	tool := newTool()
	tool.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, []byte("too big")), nil })
	if _, err := tool.fetch(t.Context(), "https://example.invalid", 2); err == nil {
		t.Fatal("oversized response accepted")
	}
	tool.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network failure") })
	if _, err := tool.previousRepository(t.Context(), "https://example.invalid", t.TempDir(), true); err == nil {
		t.Fatal("bootstrap ignored network failure")
	}
}

func TestVerifyBeforeExtractionAndCleanup(t *testing.T) {
	tool := newTool()
	called := false
	tool.run = func(_ context.Context, c command) ([]byte, error) {
		if slices.Contains(c.args, "--decrypt") {
			called = true
		}
		return nil, errors.New("unsigned")
	}
	if _, err := tool.verifiedRelease(t.Context(), "unused", []byte("unsigned literal data")); err == nil || called {
		t.Fatal("unsigned message reached extraction")
	}
	var home string
	tool.run = func(_ context.Context, c command) ([]byte, error) {
		if c.name == "gpg" {
			home = c.args[1]
			return nil, errors.New("import failed")
		}
		return nil, nil
	}
	if _, _, err := tool.keyring(t.Context(), []byte("bad key")); err == nil {
		t.Fatal("bad import accepted")
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed import leaked temporary keyring: %v", err)
	}
}

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"build"}, {"build", "--site", "x", "--url", "https://example.invalid", "--repository", "bad/repo/extra"}, {"smoke", "--site", "x", "extra"}} {
		if err := execute(t.Context(), args, io.Discard); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, args := range [][]string{{"--help"}, {"build", "--help"}} {
		if err := execute(t.Context(), args, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommandSecretEnvironment(t *testing.T) {
	if os.Getenv("APT_TOOL_TEST_HELPER") == "1" {
		if os.Getenv("APT_SIGNING_KEY") != "" || os.Getenv("APT_SIGNING_PASSPHRASE") != "" {
			os.Exit(2)
		}
		fmt.Print("clean")
		os.Exit(0)
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "APT_TOOL_TEST_HELPER=1", "APT_SIGNING_KEY=fixture-key", "APT_SIGNING_PASSPHRASE=fixture-passphrase")
	out, err := runCommand(t.Context(), command{name: program, args: []string{"-test.run=^TestCommandSecretEnvironment$"}, env: env})
	if err != nil || string(out) != "clean" {
		t.Fatalf("child environment: %q %v", out, err)
	}
}

func TestReadLimitAndAPTIsolation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(file, []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLimited(file, 3); err == nil {
		t.Fatal("read limit not enforced")
	}
	config := aptConfig("/isolated", "amd64")
	for _, expected := range []string{`Dir::Etc::main "/dev/null";`, `Dir::Etc::parts "/isolated/empty";`, `Dir::Etc::sourceparts "/isolated/empty";`, `Dir::State::status "/isolated/status";`, `#clear APT::Architectures;`, `APT::Architectures { "amd64"; };`} {
		if !strings.Contains(config, expected) {
			t.Errorf("missing APT isolation %s", expected)
		}
	}
}
