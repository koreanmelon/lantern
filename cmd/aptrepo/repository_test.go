package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// External APT/GPG tools are modeled here so validation and workflow control
// tests run on every platform. Real-tool and socket tests live in integration.
func fixtureTool(t *testing.T, releases *[]release) *tool {
	t.Helper()
	tool := newTool()
	tool.now = func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) }
	tool.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(404, nil), nil })
	tool.run = func(_ context.Context, c command) ([]byte, error) {
		switch c.name {
		case "gh":
			if c.args[0] == "api" {
				return json.Marshal([][]release{*releases})
			}
			tag := c.args[2]
			dir := c.args[slices.Index(c.args, "--dir")+1]
			for _, r := range *releases {
				if r.Tag != tag {
					continue
				}
				var manifest strings.Builder
				for _, asset := range r.Assets {
					if asset.Name == "SHA256SUMS" {
						continue
					}
					data := []byte("fixture archive " + asset.Name)
					if err := write(filepath.Join(dir, asset.Name), data); err != nil {
						return nil, err
					}
					fmt.Fprintf(&manifest, "%s  ./%s\n", digest(data), asset.Name)
				}
				return nil, write(filepath.Join(dir, "SHA256SUMS"), []byte(manifest.String()))
			}
			return nil, fmt.Errorf("unknown fixture release")
		case "dpkg-deb":
			parts := strings.Split(filepath.Base(c.args[1]), "_")
			fields := map[string]string{"Package": "lantern", "Version": parts[1], "Architecture": strings.TrimSuffix(parts[2], ".deb")}
			return []byte(fields[c.args[2]] + "\n"), nil
		case "apt-ftparchive":
			if c.args[0] == "--arch" {
				arch := c.args[1]
				paths, err := filepath.Glob(filepath.Join(c.dir, "pool/main/l/lantern/*_"+arch+".deb"))
				if err != nil {
					return nil, err
				}
				var out strings.Builder
				for _, path := range paths {
					data, err := os.ReadFile(path)
					if err != nil {
						return nil, err
					}
					fmt.Fprintf(&out, "Filename: pool/main/l/lantern/%s\nSHA256: %s\n\n", filepath.Base(path), digest(data))
				}
				return []byte(out.String()), nil
			}
			if !slices.Contains(c.args, "APT::FTPArchive::Release::Patterns::=catalog.json") {
				return nil, fmt.Errorf("retention catalog must be explicitly indexed")
			}
			var names []string
			root := filepath.Join(c.dir, "dists/stable")
			if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() && !strings.Contains(path, string(filepath.Separator)+"by-hash"+string(filepath.Separator)) {
					name, relErr := filepath.Rel(root, path)
					if relErr != nil {
						return relErr
					}
					names = append(names, filepath.ToSlash(name))
				}
				return err
			}); err != nil {
				return nil, err
			}
			sort.Strings(names)
			var out strings.Builder
			out.WriteString("Origin: Lantern\nAcquire-By-Hash: yes\nSHA256:\n")
			for _, name := range names {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&out, " %s %d %s\n", digest(data), len(data), name)
			}
			return []byte(out.String()), nil
		case "gpgconf":
			return nil, nil
		case "gpg":
			if slices.Contains(c.args, "--list-keys") || slices.Contains(c.args, "--list-secret-keys") {
				kind := "pub"
				if slices.Contains(c.args, "--list-secret-keys") {
					kind = "sec"
				}
				return []byte(kind + ":u:3072:1:key:1:2:::::sc:\nfpr:::::::::" + tool.fingerprint + ":\n"), nil
			}
			if slices.Contains(c.args, "--verify") && c.input != nil && !bytes.HasPrefix(c.input, []byte("signed\n")) {
				return nil, fmt.Errorf("fixture signature invalid")
			}
			if slices.Contains(c.args, "--decrypt") {
				return bytes.TrimPrefix(c.input, []byte("signed\n")), nil
			}
			if slices.Contains(c.args, "--clearsign") || slices.Contains(c.args, "--detach-sign") {
				if string(c.input) != "fixture-passphrase\n" {
					return nil, fmt.Errorf("fixture passphrase incorrect")
				}
				data, err := os.ReadFile(c.args[len(c.args)-1])
				if err != nil {
					return nil, err
				}
				return nil, write(c.args[slices.Index(c.args, "--output")+1], append([]byte("signed\n"), data...))
			}
			if slices.Contains(c.args, "--export") {
				return []byte("fixture public key"), nil
			}
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected fixture command %s", c.name)
		}
	}
	return tool
}

func TestBuildSignAndRetain(t *testing.T) {
	releases := []release{testRelease("v0.1.0", "arm64", "armhf"), testRelease("v0.1.5", architectures...), testRelease("v0.1.6", architectures...)}
	releases[2].Draft = true
	tool := fixtureTool(t, &releases)
	site := filepath.Join(t.TempDir(), "site")
	if err := tool.build(t.Context(), site, "test/lantern", "https://previous.invalid", true); err != nil {
		t.Fatal(err)
	}
	c, err := verifySite(site)
	if err != nil || len(c.Packages) != 5 {
		t.Fatalf("mixed history: %+v %v", c, err)
	}
	for _, arch := range architectures {
		data, err := os.ReadFile(filepath.Join(site, "dists/stable/main/binary-"+arch+"/Packages.gz"))
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		original, _ := os.ReadFile(filepath.Join(site, "dists/stable/main/binary-"+arch+"/Packages"))
		if !bytes.Equal(plain, original) {
			t.Fatal("compressed index differs from plain Packages")
		}
	}
	metadata, _ := os.ReadFile(filepath.Join(site, "dists/stable/Release"))
	if !bytes.Contains(metadata, []byte("Valid-Until: Fri, 20 Nov 2026 09:00:00 +0000")) {
		t.Fatal("metadata does not expire in 30 days")
	}
	if tool.sign(t.Context(), site, []byte("fixture key"), []byte("wrong")) == nil {
		t.Fatal("wrong passphrase accepted")
	}
	if err := tool.sign(t.Context(), site, []byte("fixture key"), []byte("fixture-passphrase")); err != nil {
		t.Fatal(err)
	}
	tool.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, err := os.ReadFile(filepath.Join(site, filepath.FromSlash(strings.TrimPrefix(req.URL.Path, "/"))))
		if err != nil {
			return response(404, nil), nil
		}
		return response(200, data), nil
	})
	previous, err := tool.previousRepository(t.Context(), "https://previous.invalid", filepath.Join(t.TempDir(), "copy"), false)
	if err != nil || !reflect.DeepEqual(c, previous) {
		t.Fatalf("previous history: %v %v", previous, err)
	}
	releases[2].Draft = false
	second := filepath.Join(t.TempDir(), "second")
	if err := tool.build(t.Context(), second, "test/lantern", "https://previous.invalid", false); err != nil {
		t.Fatal(err)
	}
	current, err := verifySite(second)
	if err != nil || len(current.Packages) != 8 {
		t.Fatalf("second generation: %v %v", current, err)
	}
	if err := checkRetention(c, current); err != nil {
		t.Fatal(err)
	}
	for name, sha := range c.Indexes {
		if current.Indexes[name] != sha {
			t.Fatalf("lost historical index %s", name)
		}
	}
	manifest := filepath.Join(site, "dists/stable/catalog.json")
	saved, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, append(saved, ' '), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.previousRepository(t.Context(), "https://previous.invalid", t.TempDir(), false); err == nil {
		t.Fatal("tampered signed catalog accepted")
	}
	if err := os.WriteFile(manifest, saved, 0644); err != nil {
		t.Fatal(err)
	}
	for name := range c.Packages {
		if err := os.WriteFile(filepath.Join(site, filepath.FromSlash(name)), []byte("corrupt"), 0644); err != nil {
			t.Fatal(err)
		}
		break
	}
	if _, err := verifySite(site); err == nil {
		t.Fatal("corrupt package accepted")
	}
}

func TestBuildFailures(t *testing.T) {
	for _, fault := range []string{"checksum", "architecture", "missing-architecture", "extra-file", "symlink", "existing-output"} {
		t.Run(fault, func(t *testing.T) {
			releases := []release{testRelease("v0.1.5", architectures...)}
			tool := fixtureTool(t, &releases)
			original := tool.run
			tool.run = func(ctx context.Context, c command) ([]byte, error) {
				if fault == "architecture" && c.name == "dpkg-deb" && c.args[2] == "Architecture" {
					return []byte("wrong"), nil
				}
				out, err := original(ctx, c)
				if err == nil && fault == "checksum" && c.name == "gh" && c.args[0] == "release" {
					dir := c.args[slices.Index(c.args, "--dir")+1]
					err = os.WriteFile(filepath.Join(dir, "lantern_0.1.5-1_amd64.deb"), []byte("tampered"), 0644)
				}
				return out, err
			}
			if fault == "missing-architecture" {
				releases[0] = testRelease("v0.1.5", "arm64", "armhf")
			}
			site := filepath.Join(t.TempDir(), "site")
			if fault == "existing-output" {
				if err := os.Mkdir(site, 0755); err != nil {
					t.Fatal(err)
				}
			}
			err := tool.build(t.Context(), site, "test/lantern", "https://previous.invalid", true)
			if fault == "extra-file" || fault == "symlink" {
				if err != nil {
					t.Fatal(err)
				}
				if fault == "extra-file" {
					err = os.WriteFile(filepath.Join(site, "private-key.asc"), []byte("never deploy"), 0600)
				} else {
					err = os.Symlink("index.html", filepath.Join(site, "link"))
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = verifySite(site)
			}
			if err == nil {
				t.Fatalf("accepted %s", fault)
			}
		})
	}
}
