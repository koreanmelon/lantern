package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"github.com/koreanmelon/lantern/pkg/webui"
	"github.com/spf13/cobra"
)

func newRootCommand(serve func(context.Context, string, string) error) *cobra.Command {
	var address, webAddress string
	cmd := &cobra.Command{
		Use:           "lantern",
		Short:         "Run Lantern DNS and its web-based record manager",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log.New(cmd.ErrOrStderr(), "", log.LstdFlags).Printf("Starting Lantern: DNS %s · web http://%s", address, webAddress)
			return serve(cmd.Context(), address, webAddress)
		},
	}
	cmd.Flags().StringVar(&address, "dns-listen", dnsserver.DefaultAddress, "DNS UDP and TCP listen address")
	cmd.Flags().StringVar(&webAddress, "web-listen", webui.DefaultAddress, "Web record manager listen address (no authentication)")
	return cmd
}

func serveApplication(ctx context.Context, address, webAddress string) error {
	handler, err := exampleHandler()
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", webAddress)
	if err != nil {
		return fmt.Errorf("listen web UI: %w", err)
	}
	defer listener.Close()
	host, _, _ := net.SplitHostPort(webAddress)
	ui, err := webui.New(handler, host)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := &http.Server{
		Handler: ui, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}
	s := dnsserver.Server{
		Config: dnsserver.Config{
			Address:        address,
			Handler:        handler.Handle,
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			HandlerTimeout: 10 * time.Second,
			Concurrency:    10,
		},
	}
	results := make(chan error, 2)
	go func() { results <- s.ListenAndServe(runCtx) }()
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		results <- err
	}()
	first := <-results
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	return errors.Join(first, <-results, shutdownErr)
}
