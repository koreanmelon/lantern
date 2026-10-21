// aptrepo generates, signs, and tests Lantern's static APT repository.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Tests can embed a disposable fingerprint with -ldflags. Production builds use
// this pinned identity; neither flags nor environment can override it at runtime.
var signingFingerprint = "C4B45272C3850D898F9B0A42AF494EF0B97CBBAC"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "aptrepo:", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: aptrepo {build|sign|smoke} --site DIRECTORY [options]")
	}
	operation := args[0]
	if operation == "--help" || operation == "-h" {
		fmt.Fprintln(stderr, "usage: aptrepo {build|sign|smoke} --site DIRECTORY [--repository OWNER/REPO] [--url URL] [--bootstrap]")
		return nil
	}
	if operation != "build" && operation != "sign" && operation != "smoke" {
		return fmt.Errorf("unknown command %q", operation)
	}
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	site := flags.String("site", "", "repository output directory (required)")
	repository := flags.String("repository", "koreanmelon/lantern", "GitHub owner/repository")
	url := flags.String("url", "", "existing repository or live smoke-test URL")
	bootstrap := flags.Bool("bootstrap", false, "allow a missing previous InRelease (HTTP 404 only)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *site == "" || flags.NArg() != 0 {
		return fmt.Errorf("--site is required; positional arguments are not accepted")
	}
	root, err := filepath.Abs(*site)
	if err != nil {
		return err
	}
	t := newTool()
	switch operation {
	case "build":
		if !repositoryPattern.MatchString(*repository) || *url == "" {
			return fmt.Errorf("build requires --url and a valid --repository")
		}
		return t.build(ctx, root, *repository, *url, *bootstrap)
	case "sign":
		key, keyOK := os.LookupEnv("APT_SIGNING_KEY")
		passphrase, passOK := os.LookupEnv("APT_SIGNING_PASSPHRASE")
		// The runner also filters these out of every child process environment.
		os.Unsetenv("APT_SIGNING_KEY")
		os.Unsetenv("APT_SIGNING_PASSPHRASE")
		if !keyOK || key == "" || !passOK {
			return fmt.Errorf("APT_SIGNING_KEY and APT_SIGNING_PASSPHRASE must be set")
		}
		return t.sign(ctx, root, []byte(key), []byte(passphrase))
	default:
		return t.smoke(ctx, root, *url)
	}
}
