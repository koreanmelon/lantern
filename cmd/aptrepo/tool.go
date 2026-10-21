package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type command struct {
	name  string
	args  []string
	dir   string
	input []byte
	env   []string
}

type tool struct {
	run         func(context.Context, command) ([]byte, error)
	client      *http.Client
	now         func() time.Time
	fingerprint string
}

func newTool() *tool {
	return &tool{run: runCommand, client: &http.Client{Timeout: 60 * time.Second}, now: time.Now, fingerprint: signingFingerprint}
}

func childEnvironment(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	clean := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "APT_SIGNING_KEY" && key != "APT_SIGNING_PASSPHRASE" {
			clean = append(clean, entry)
		}
	}
	return clean
}

func runCommand(ctx context.Context, c command) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.name, c.args...)
	cmd.Dir = c.dir
	cmd.Env = childEnvironment(c.env)
	cmd.Stdin = bytes.NewReader(c.input)
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.Output()
	if err != nil {
		// Never include input or environment values: they can contain secrets.
		return nil, fmt.Errorf("%s failed: %w", c.name, err)
	}
	return output, nil
}

func (t *tool) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	return t.run(ctx, command{name: name, args: args})
}

type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.url) }

func (t *tool) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &statusError{url: url, code: response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download exceeds size limit: %s", url)
	}
	return data, nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds size limit: %s", path)
	}
	return data, nil
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func indexDigest(algorithm string, data []byte) string {
	if algorithm == "SHA512" {
		return fmt.Sprintf("%x", sha512.Sum512(data))
	}
	return digest(data)
}
