package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	tagPattern        = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	poolPattern       = regexp.MustCompile(`^pool/main/l/lantern/lantern_[0-9]+\.[0-9]+\.[0-9]+-[1-9][0-9]*_(amd64|arm64|armhf)\.deb$`)
	byHashPattern     = regexp.MustCompile(`^dists/stable/main/binary-(amd64|arm64|armhf)/by-hash/(SHA256/[0-9a-f]{64}|SHA512/[0-9a-f]{128})$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	checksumPattern   = regexp.MustCompile(`^([0-9a-f]{64})  (\./)?([A-Za-z0-9_.-]+)$`)
)

var architectures = []string{"amd64", "arm64", "armhf"}

const (
	metadataLimit = 16 * 1024 * 1024
	packageLimit  = 64 * 1024 * 1024
	siteLimit     = 900 * 1024 * 1024
)

type packageEntry struct {
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
}

type catalog struct {
	Packages map[string]packageEntry `json:"packages"`
	Indexes  map[string]string       `json:"indexes"`
}

func emptyCatalog() catalog {
	return catalog{Packages: map[string]packageEntry{}, Indexes: map[string]string{}}
}

func decodeCatalog(data []byte) (catalog, error) {
	top, err := objectFields(data, "packages", "indexes")
	if err != nil {
		return catalog{}, err
	}
	packages, err := objectFields(top["packages"])
	if err != nil {
		return catalog{}, err
	}
	indexes, err := objectFields(top["indexes"])
	if err != nil {
		return catalog{}, err
	}
	c := emptyCatalog()
	for name, raw := range packages {
		if _, err := objectFields(raw, "sha256", "size", "version", "architecture"); err != nil {
			return catalog{}, fmt.Errorf("package %s: %w", name, err)
		}
		var entry packageEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return catalog{}, err
		}
		c.Packages[name] = entry
	}
	for name, raw := range indexes {
		var sha string
		if err := json.Unmarshal(raw, &sha); err != nil {
			return catalog{}, err
		}
		c.Indexes[name] = sha
	}
	return c, validateCatalog(c)
}

// Struct decoding alone accepts case aliases and repeated fields. Keep schema
// names exact, reject duplicate keys, and require every structured field.
func objectFields(data []byte, required ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("catalog requires JSON objects, not null or scalar values")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid catalog object key")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate catalog key: %s", name)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("null catalog field: %s", name)
		}
		fields[name] = raw
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, fmt.Errorf("invalid catalog object ending")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("catalog has trailing data")
	}
	if len(required) > 0 {
		if len(fields) != len(required) {
			return nil, fmt.Errorf("catalog object has missing or unknown fields")
		}
		for _, name := range required {
			if _, ok := fields[name]; !ok {
				return nil, fmt.Errorf("missing exact catalog field: %s", name)
			}
		}
	}
	return fields, nil
}

func validateCatalog(c catalog) error {
	if c.Packages == nil || c.Indexes == nil {
		return fmt.Errorf("catalog requires packages and indexes objects")
	}
	for name, entry := range c.Packages {
		if !poolPattern.MatchString(name) || !sha256Pattern.MatchString(entry.SHA256) || entry.Size <= 0 ||
			name != fmt.Sprintf("pool/main/l/lantern/lantern_%s_%s.deb", entry.Version, entry.Architecture) {
			return fmt.Errorf("invalid package catalog entry: %s", name)
		}
	}
	for name, sha := range c.Indexes {
		if !byHashPattern.MatchString(name) || !sha256Pattern.MatchString(sha) {
			return fmt.Errorf("invalid by-hash catalog entry: %s", name)
		}
	}
	return nil
}

func verifyIndex(name string, data []byte, sha string) error {
	if !byHashPattern.MatchString(name) {
		return fmt.Errorf("invalid by-hash path: %s", name)
	}
	parts := strings.Split(name, "/")
	if digest(data) != sha || indexDigest(parts[len(parts)-2], data) != parts[len(parts)-1] {
		return fmt.Errorf("historical by-hash index was modified: %s", name)
	}
	return nil
}

func checkRetention(previous, current catalog) error {
	for name, entry := range previous.Packages {
		if current.Packages[name] != entry {
			return fmt.Errorf("previously published package missing or replaced: %s", name)
		}
	}
	return nil
}

type hashEntry struct {
	sha  string
	size int64
}

func releaseHashes(data []byte) (map[string]hashEntry, error) {
	entries := map[string]hashEntry{}
	active := false
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "SHA256:" {
			active = true
			continue
		}
		if !strings.HasPrefix(line, " ") {
			active = false
		}
		if !active {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || !sha256Pattern.MatchString(fields[0]) {
			return nil, fmt.Errorf("invalid Release SHA256 table")
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("invalid Release file size")
		}
		if _, exists := entries[fields[2]]; exists {
			return nil, fmt.Errorf("duplicate Release hash entry: %s", fields[2])
		}
		entries[fields[2]] = hashEntry{sha: fields[0], size: size}
	}
	return entries, nil
}

func checksums(data []byte, names []string) (map[string]string, error) {
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		match := checksumPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("malformed SHA256SUMS entry")
		}
		if _, exists := result[match[3]]; exists {
			return nil, fmt.Errorf("duplicate SHA256SUMS entry")
		}
		result[match[3]] = match[1]
	}
	if len(result) != len(names) {
		return nil, fmt.Errorf("SHA256SUMS must cover exactly the selected packages")
	}
	for _, name := range names {
		if _, exists := result[name]; !exists {
			return nil, fmt.Errorf("missing package checksum: %s", name)
		}
	}
	return result, nil
}

type releaseAsset struct {
	Name string `json:"name"`
}

type release struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type selectedPackage struct {
	name    string
	version string
	arch    string
}

// Compare validated stable tags without integer overflow on version components.
func compareTags(left, right string) int {
	a := strings.Split(strings.TrimPrefix(left, "v"), ".")
	b := strings.Split(strings.TrimPrefix(right, "v"), ".")
	for i := range 3 {
		if len(a[i]) != len(b[i]) {
			return len(a[i]) - len(b[i])
		}
		if result := strings.Compare(a[i], b[i]); result != 0 {
			return result
		}
	}
	return 0
}

func releasePackages(r release) ([]selectedPackage, error) {
	if !tagPattern.MatchString(r.Tag) {
		return nil, fmt.Errorf("invalid stable tag: %s", r.Tag)
	}
	names := map[string]bool{}
	for _, asset := range r.Assets {
		if names[asset.Name] {
			return nil, fmt.Errorf("duplicate release asset: %s", asset.Name)
		}
		names[asset.Name] = true
	}
	if !names["SHA256SUMS"] {
		return nil, fmt.Errorf("release %s has no SHA256SUMS", r.Tag)
	}
	var selected []selectedPackage
	version := ""
	for _, arch := range architectures {
		pattern := regexp.MustCompile(`^lantern_(` + regexp.QuoteMeta(r.Tag[1:]) + `-[1-9][0-9]*)_` + arch + `\.deb$`)
		var matches []selectedPackage
		for name := range names {
			if match := pattern.FindStringSubmatch(name); match != nil {
				matches = append(matches, selectedPackage{name: name, version: match[1], arch: arch})
			}
		}
		if arch == "amd64" && compareTags(r.Tag, "v0.1.5") < 0 && len(matches) == 0 {
			continue
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("expected one %s package in %s", arch, r.Tag)
		}
		if version != "" && matches[0].version != version {
			return nil, fmt.Errorf("architecture package versions disagree in %s", r.Tag)
		}
		version = matches[0].version
		selected = append(selected, matches[0])
	}
	return selected, nil
}

func publishedReleases(data []byte) ([]release, error) {
	var pages [][]release
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, err
	}
	var selected []release
	seen := map[string]bool{}
	for _, page := range pages {
		for _, r := range page {
			if r.Draft || r.Prerelease || !tagPattern.MatchString(r.Tag) {
				continue
			}
			if seen[r.Tag] {
				return nil, fmt.Errorf("duplicate release tag: %s", r.Tag)
			}
			seen[r.Tag] = true
			selected = append(selected, r)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no published stable releases")
	}
	sort.Slice(selected, func(i, j int) bool { return compareTags(selected[i].Tag, selected[j].Tag) < 0 })
	return selected, nil
}
