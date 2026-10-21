//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Opt in after building a package. This only inspects archives: no package,
// maintainer script, or target executable is installed or run.
func TestDebianPackage(t *testing.T) {
	path := os.Getenv("LANTERN_DEB_FILE")
	if path == "" {
		t.Skip("set LANTERN_DEB_FILE to an absolute .deb path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("!<arch>\n")) {
		t.Fatal("not a Debian ar archive")
	}
	members := make(map[string][]byte)
	for offset := 8; offset < len(data); {
		if len(data)-offset < 60 {
			t.Fatal("truncated ar header")
		}
		header := data[offset : offset+60]
		if string(header[58:60]) != "`\n" {
			t.Fatal("invalid ar header terminator")
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(header[:16])), "/")
		size, err := strconv.Atoi(strings.TrimSpace(string(header[48:58])))
		if err != nil || size < 0 || size > len(data)-offset-60 {
			t.Fatal("invalid ar member size")
		}
		if _, exists := members[name]; exists {
			t.Fatalf("duplicate member %s", name)
		}
		offset += 60
		members[name] = data[offset : offset+size]
		offset += size + size%2
	}
	if string(members["debian-binary"]) != "2.0\n" {
		t.Fatal("incorrect Debian archive version")
	}
	control := unpackTarGzip(t, members["control.tar.gz"])
	payload := unpackTarGzip(t, members["data.tar.gz"])
	metadata := string(control["control"].data)
	for _, field := range []string{"Version", "Maintainer", "Architecture"} {
		value := ""
		for _, line := range strings.Split(metadata, "\n") {
			if strings.HasPrefix(line, field+":") {
				value = strings.TrimSpace(strings.TrimPrefix(line, field+":"))
				break
			}
		}
		if value == "" {
			t.Fatalf("empty required %s metadata", field)
		}
	}
	for _, field := range []string{"Package: lantern\n", "Version: ", "Maintainer: ", "Depends: systemd (>= 235)"} {
		if !strings.Contains(metadata, field) {
			t.Fatalf("missing metadata %q:\n%s", field, metadata)
		}
	}
	if !strings.Contains(string(control["conffiles"].data), "/etc/lantern/lantern.yaml") {
		t.Fatal("startup config is not marked as a conffile")
	}
	for _, name := range []string{"preinst", "prerm"} {
		if _, exists := control[name]; exists {
			t.Fatalf("unexpected lifecycle script %s", name)
		}
	}
	for _, name := range []string{"postinst", "postrm"} {
		entry, exists := control[name]
		if !exists || entry.mode != 0755 || entry.uid != 0 || entry.gid != 0 {
			t.Fatalf("missing or incorrect mode/ownership for %s", name)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "build", "package", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(entry.data, want) {
			t.Fatalf("package differs from canonical %s", name)
		}
	}
	for _, file := range []struct {
		installed, source string
		mode              int64
	}{
		{"usr/bin/lantern", "", 0755},
		{"etc/lantern/lantern.yaml", "configs/lantern.yaml", 0644},
		{"usr/lib/systemd/system/lantern.service", "init/lantern.service", 0644},
		{"usr/share/doc/lantern/README.md", "README.md", 0644},
		{"usr/share/doc/lantern/copyright", "LICENSE", 0644},
		{"usr/share/doc/lantern/docs/pi-verification.md", "docs/pi-verification.md", 0644},
		{"usr/share/doc/lantern/docs/apt-repository.md", "docs/apt-repository.md", 0644},
	} {
		entry, exists := payload[file.installed]
		if !exists {
			t.Fatalf("missing %s", file.installed)
		}
		if entry.mode != file.mode || entry.uid != 0 || entry.gid != 0 {
			t.Fatalf("incorrect mode/ownership for %s", file.installed)
		}
		if file.source != "" {
			want, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(file.source)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(entry.data, want) {
				t.Fatalf("package differs from canonical %s", file.source)
			}
		}
	}
	if !bytes.Contains(payload["usr/lib/systemd/system/lantern.service"].data, []byte("ExecStart=/usr/bin/lantern --config /etc/lantern/lantern.yaml")) {
		t.Fatal("unit does not execute the packaged binary/config")
	}
	program, err := elf.NewFile(bytes.NewReader(payload["usr/bin/lantern"].data))
	if err != nil {
		t.Fatalf("packaged binary is not ELF: %v", err)
	}
	defer program.Close()
	switch {
	case strings.Contains(metadata, "Architecture: amd64\n"):
		if program.Machine != elf.EM_X86_64 || program.Class != elf.ELFCLASS64 {
			t.Fatal("amd64 package contains wrong binary architecture")
		}
	case strings.Contains(metadata, "Architecture: arm64\n"):
		if program.Machine != elf.EM_AARCH64 || program.Class != elf.ELFCLASS64 {
			t.Fatal("arm64 package contains wrong binary architecture")
		}
	case strings.Contains(metadata, "Architecture: armhf\n"):
		if program.Machine != elf.EM_ARM || program.Class != elf.ELFCLASS32 {
			t.Fatal("armhf package contains wrong binary architecture")
		}
	default:
		t.Fatal("expected an amd64, arm64, or armhf package")
	}
	for _, segment := range program.Progs {
		if segment.Type == elf.PT_INTERP {
			t.Fatal("binary is dynamically linked; build with CGO_ENABLED=0")
		}
	}
}

type packagedFile struct {
	data     []byte
	mode     int64
	uid, gid int
}

func unpackTarGzip(t *testing.T, data []byte) map[string]packagedFile {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	files := make(map[string]packagedFile)
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(header.Name, "./"), "/")
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = packagedFile{data: content, mode: header.Mode & 0777, uid: header.Uid, gid: header.Gid}
	}
	return files
}
