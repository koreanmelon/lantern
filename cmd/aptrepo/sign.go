package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func checkFingerprint(data []byte, expected string) error {
	var fingerprints []string
	primary := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "pub", "sec":
			if fields[1] == "r" || fields[1] == "e" || fields[1] == "d" {
				return fmt.Errorf("signing key is revoked, expired, or disabled")
			}
			primary = true
		case "fpr":
			if primary {
				if len(fields) < 10 {
					return fmt.Errorf("invalid GnuPG fingerprint output")
				}
				fingerprints = append(fingerprints, fields[9])
				primary = false
			}
		}
	}
	if len(fingerprints) != 1 || fingerprints[0] != expected {
		return fmt.Errorf("keyring must contain exactly the pinned signing key")
	}
	return nil
}

func (t *tool) verifyFingerprint(ctx context.Context, home string, secret bool) error {
	operation := "--list-keys"
	if secret {
		operation = "--list-secret-keys"
	}
	output, err := t.command(ctx, "gpg", "--homedir", home, "--batch", "--with-colons", operation)
	if err != nil {
		return err
	}
	return checkFingerprint(output, t.fingerprint)
}

func (t *tool) keyring(ctx context.Context, key []byte) (string, func(), error) {
	home, err := os.MkdirTemp("", "g-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = t.command(cleanupCtx, "gpgconf", "--homedir", home, "--kill", "gpg-agent")
		_ = os.RemoveAll(home)
	}
	_, err = t.run(ctx, command{name: "gpg", args: []string{"--homedir", home, "--batch", "--import"}, input: key})
	if err == nil {
		err = t.verifyFingerprint(ctx, home, false)
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return home, cleanup, nil
}

func (t *tool) verifiedRelease(ctx context.Context, home string, signed []byte) ([]byte, error) {
	// --decrypt alone also accepts unsigned OpenPGP literal-data packets.
	// Verify these exact bytes using the single pinned primary key first.
	args := []string{"--homedir", home, "--batch", "--no-auto-key-retrieve"}
	verify := append(append([]string{}, args...), "--verify")
	if _, err := t.run(ctx, command{name: "gpg", args: verify, input: signed}); err != nil {
		return nil, err
	}
	return t.run(ctx, command{name: "gpg", args: append(args, "--decrypt"), input: signed})
}

func (t *tool) sign(ctx context.Context, site string, key, passphrase []byte) error {
	if _, err := verifySite(site); err != nil {
		return err
	}
	if bytes.ContainsAny(passphrase, "\r\n") {
		return fmt.Errorf("signing passphrase must not contain newlines")
	}
	home, cleanup, err := t.keyring(ctx, key)
	if err != nil {
		return err
	}
	// Clean up before opening a separate public-only verification keyring.
	public, err := func() ([]byte, error) {
		defer cleanup()
		if err := t.verifyFingerprint(ctx, home, true); err != nil {
			return nil, err
		}
		release := filepath.Join(site, "dists/stable/Release")
		for _, operation := range [][2]string{{"InRelease", "--clearsign"}, {"Release.gpg", "--detach-sign"}} {
			args := []string{"--homedir", home, "--batch", "--yes", "--no-tty", "--pinentry-mode", "loopback", "--passphrase-fd", "0", "--local-user", t.fingerprint,
				"--digest-algo", "SHA256", "--armor", "--output", filepath.Join(site, "dists/stable", operation[0]), operation[1], release}
			input := append(append([]byte{}, passphrase...), '\n')
			if _, err := t.run(ctx, command{name: "gpg", args: args, input: input}); err != nil {
				return nil, err
			}
		}
		return t.command(ctx, "gpg", "--homedir", home, "--armor", "--export", t.fingerprint)
	}()
	if err != nil {
		return err
	}
	if err := write(filepath.Join(site, "lantern-archive-keyring.asc"), public); err != nil {
		return err
	}
	home, cleanup, err = t.keyring(ctx, public)
	if err != nil {
		return err
	}
	defer cleanup()
	signed, err := readLimited(filepath.Join(site, "dists/stable/InRelease"), metadataLimit)
	if err != nil {
		return err
	}
	plain, err := t.verifiedRelease(ctx, home, signed)
	if err != nil {
		return err
	}
	releasePath := filepath.Join(site, "dists/stable/Release")
	release, err := readLimited(releasePath, metadataLimit)
	if err != nil {
		return err
	}
	if !bytes.Equal(plain, release) {
		return fmt.Errorf("signed Release content mismatch")
	}
	if _, err := t.command(ctx, "gpg", "--homedir", home, "--batch", "--verify", filepath.Join(site, "dists/stable/Release.gpg"), releasePath); err != nil {
		return err
	}
	_, err = verifySite(site)
	return err
}
