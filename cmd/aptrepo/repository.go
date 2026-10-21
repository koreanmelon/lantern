package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (t *tool) previousRepository(ctx context.Context, url, site string, bootstrap bool) (catalog, error) {
	url = strings.TrimRight(url, "/")
	signed, err := t.fetch(ctx, url+"/dists/stable/InRelease", metadataLimit)
	if err != nil {
		var status *statusError
		if bootstrap && errors.As(err, &status) && status.code == http.StatusNotFound {
			return emptyCatalog(), nil
		}
		return catalog{}, err
	}
	public, err := t.fetch(ctx, url+"/lantern-archive-keyring.asc", metadataLimit)
	if err != nil {
		return catalog{}, err
	}
	home, cleanup, err := t.keyring(ctx, public)
	if err != nil {
		return catalog{}, err
	}
	defer cleanup()
	release, err := t.verifiedRelease(ctx, home, signed)
	if err != nil {
		return catalog{}, err
	}
	data, err := t.fetch(ctx, url+"/dists/stable/catalog.json", metadataLimit)
	if err != nil {
		return catalog{}, err
	}
	hashes, err := releaseHashes(release)
	if err != nil {
		return catalog{}, err
	}
	if hashes["catalog.json"] != (hashEntry{sha: digest(data), size: int64(len(data))}) {
		return catalog{}, fmt.Errorf("previous catalog does not match signed metadata")
	}
	previous, err := decodeCatalog(data)
	if err != nil {
		return catalog{}, err
	}
	for name, sha := range previous.Indexes {
		data, err := t.fetch(ctx, url+"/"+name, metadataLimit)
		if err != nil {
			return catalog{}, err
		}
		if err := verifyIndex(name, data, sha); err != nil {
			return catalog{}, err
		}
		if err := write(filepath.Join(site, filepath.FromSlash(name)), data); err != nil {
			return catalog{}, err
		}
	}
	return previous, nil
}

func (t *tool) build(ctx context.Context, site, repository, url string, bootstrap bool) error {
	if err := os.MkdirAll(filepath.Dir(site), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(site, 0755); err != nil {
		return fmt.Errorf("output directory must not already exist: %w", err)
	}
	previous, err := t.previousRepository(ctx, url, site, bootstrap)
	if err != nil {
		return err
	}
	data, err := t.command(ctx, "gh", "api", "--paginate", "--slurp", "repos/"+repository+"/releases?per_page=100")
	if err != nil {
		return err
	}
	releases, err := publishedReleases(data)
	if err != nil {
		return err
	}
	c := emptyCatalog()
	c.Indexes = previous.Indexes
	for _, release := range releases {
		if err := t.downloadRelease(ctx, site, repository, release, c); err != nil {
			return err
		}
	}
	if err := checkRetention(previous, c); err != nil {
		return err
	}
	if err := t.generateIndexes(ctx, site, c); err != nil {
		return err
	}
	if err := write(filepath.Join(site, ".nojekyll"), nil); err != nil {
		return err
	}
	const landing = "<!doctype html><title>Lantern APT repository</title><h1>Lantern APT repository</h1><p>Signed stable packages for amd64, arm64, and armhf.</p><p><a href='https://github.com/koreanmelon/lantern/blob/main/docs/apt-repository.md'>Setup instructions</a></p>\n"
	if err := write(filepath.Join(site, "index.html"), []byte(landing)); err != nil {
		return err
	}
	_, err = verifySite(site)
	return err
}

func (t *tool) downloadRelease(ctx context.Context, site, repository string, r release, c catalog) error {
	selected, err := releasePackages(r)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "apt-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	args := []string{"release", "download", r.Tag, "--repo", repository, "--dir", dir, "--pattern", "SHA256SUMS"}
	names := make([]string, 0, len(selected))
	for _, p := range selected {
		args = append(args, "--pattern", p.name)
		names = append(names, p.name)
	}
	if _, err := t.command(ctx, "gh", args...); err != nil {
		return err
	}
	data, err := readLimited(filepath.Join(dir, "SHA256SUMS"), metadataLimit)
	if err != nil {
		return err
	}
	sums, err := checksums(data, names)
	if err != nil {
		return err
	}
	for _, p := range selected {
		source := filepath.Join(dir, p.name)
		data, err := readLimited(source, packageLimit)
		if err != nil {
			return err
		}
		if digest(data) != sums[p.name] {
			return fmt.Errorf("package checksum mismatch: %s", p.name)
		}
		for _, field := range [][2]string{{"Package", "lantern"}, {"Version", p.version}, {"Architecture", p.arch}} {
			value, err := t.command(ctx, "dpkg-deb", "--field", source, field[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(value)) != field[1] {
				return fmt.Errorf("package %s mismatch: %s", field[0], p.name)
			}
		}
		name := "pool/main/l/lantern/" + p.name
		if err := write(filepath.Join(site, filepath.FromSlash(name)), data); err != nil {
			return err
		}
		c.Packages[name] = packageEntry{SHA256: digest(data), Size: int64(len(data)), Version: p.version, Architecture: p.arch}
	}
	return nil
}

func (t *tool) generateIndexes(ctx context.Context, site string, c catalog) error {
	for _, arch := range architectures {
		dir := "dists/stable/main/binary-" + arch
		packages, err := t.run(ctx, command{name: "apt-ftparchive", args: []string{"--arch", arch, "packages", "pool"}, dir: site})
		if err != nil {
			return err
		}
		var compressed bytes.Buffer
		gz, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
		if err != nil {
			return err
		}
		if _, err := gz.Write(packages); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
		for name, data := range map[string][]byte{"Packages": packages, "Packages.gz": compressed.Bytes()} {
			if err := write(filepath.Join(site, filepath.FromSlash(dir), name), data); err != nil {
				return err
			}
			for _, algorithm := range []string{"SHA256", "SHA512"} {
				relative := dir + "/by-hash/" + algorithm + "/" + indexDigest(algorithm, data)
				if err := write(filepath.Join(site, filepath.FromSlash(relative)), data); err != nil {
					return err
				}
				c.Indexes[relative] = digest(data)
			}
		}
	}
	if err := validateCatalog(c); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := write(filepath.Join(site, "dists/stable/catalog.json"), append(data, '\n')); err != nil {
		return err
	}
	args := []string{"-o", "APT::FTPArchive::Release::Patterns::=catalog.json"}
	for _, option := range [][2]string{
		{"Origin", "Lantern"}, {"Label", "Lantern"}, {"Suite", "stable"}, {"Codename", "stable"},
		{"Architectures", strings.Join(architectures, " ")}, {"Components", "main"},
		{"Acquire-By-Hash", "yes"}, {"MD5", "false"}, {"SHA1", "false"},
	} {
		args = append(args, "-o", "APT::FTPArchive::Release::"+option[0]+"="+option[1])
	}
	args = append(args, "release", "dists/stable")
	data, err = t.run(ctx, command{name: "apt-ftparchive", args: args, dir: site})
	if err != nil {
		return err
	}
	expiry := t.now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC1123Z)
	data = append([]byte("Valid-Until: "+expiry+"\n"), data...)
	return write(filepath.Join(site, "dists/stable/Release"), data)
}

func verifySite(site string) (catalog, error) {
	allowed := map[string]bool{}
	for _, name := range []string{".nojekyll", "index.html", "lantern-archive-keyring.asc", "dists/stable/Release", "dists/stable/InRelease", "dists/stable/Release.gpg", "dists/stable/catalog.json"} {
		allowed[name] = true
	}
	// Reject links/special files before opening any artifact contents.
	var size int64
	var files []string
	err := filepath.WalkDir(site, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular file in repository artifact: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		if size > siteLimit {
			return fmt.Errorf("repository approaching GitHub Pages 1 GB limit")
		}
		name, err := filepath.Rel(site, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(name))
		return nil
	})
	if err != nil {
		return catalog{}, err
	}
	data, err := readLimited(filepath.Join(site, "dists/stable/catalog.json"), metadataLimit)
	if err != nil {
		return catalog{}, err
	}
	c, err := decodeCatalog(data)
	if err != nil {
		return catalog{}, err
	}
	for name, entry := range c.Packages {
		data, err := readLimited(filepath.Join(site, filepath.FromSlash(name)), packageLimit)
		if err != nil {
			return catalog{}, err
		}
		if digest(data) != entry.SHA256 || int64(len(data)) != entry.Size {
			return catalog{}, fmt.Errorf("package changed after generation: %s", name)
		}
		allowed[name] = true
	}
	for name, sha := range c.Indexes {
		data, err := readLimited(filepath.Join(site, filepath.FromSlash(name)), metadataLimit)
		if err != nil {
			return catalog{}, err
		}
		if err := verifyIndex(name, data, sha); err != nil {
			return catalog{}, err
		}
		allowed[name] = true
	}
	data, err = readLimited(filepath.Join(site, "dists/stable/Release"), metadataLimit)
	if err != nil {
		return catalog{}, err
	}
	hashes, err := releaseHashes(data)
	if err != nil {
		return catalog{}, err
	}
	names := []string{"catalog.json"}
	for _, arch := range architectures {
		names = append(names, "main/binary-"+arch+"/Packages", "main/binary-"+arch+"/Packages.gz")
	}
	for _, name := range names {
		relative := "dists/stable/" + name
		data, err := readLimited(filepath.Join(site, filepath.FromSlash(relative)), metadataLimit)
		if err != nil {
			return catalog{}, err
		}
		if hashes[name] != (hashEntry{sha: digest(data), size: int64(len(data))}) {
			return catalog{}, fmt.Errorf("Release checksum mismatch: %s", name)
		}
		allowed[relative] = true
	}
	for _, name := range files {
		if !allowed[name] {
			return catalog{}, fmt.Errorf("unexpected repository file: %s", name)
		}
	}
	return c, nil
}
