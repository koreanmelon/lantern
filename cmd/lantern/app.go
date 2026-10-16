package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/koreanmelon/lantern/pkg/config"
	"github.com/koreanmelon/lantern/pkg/dns/server"
	"github.com/koreanmelon/lantern/pkg/webui"
)

func serveApplication(ctx context.Context, cfg config.Config) error {
	// Validate before opening either listener, including when called without Cobra.
	if err := cfg.Validate(); err != nil {
		return err
	}
	handler, err := exampleHandler()
	if err != nil {
		return err
	}
	if err := handler.EnablePersistence(cfg.RecordsFile); err != nil {
		return fmt.Errorf("record storage: %w", err)
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.Web.Address)
	if err != nil {
		return fmt.Errorf("listen web UI: %w", err)
	}
	defer listener.Close()
	host, _, _ := net.SplitHostPort(cfg.Web.Address)
	hosts := append([]string{host}, cfg.Web.AllowedHosts...)
	ui, err := webui.New(handler, hosts...)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := &http.Server{
		Handler: ui, ReadHeaderTimeout: cfg.Web.ReadHeaderTimeout, ReadTimeout: cfg.Web.ReadTimeout,
		WriteTimeout: cfg.Web.WriteTimeout, IdleTimeout: cfg.Web.IdleTimeout, MaxHeaderBytes: cfg.Web.MaxHeaderBytes,
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}
	s := dnsserver.Server{Config: dnsserver.Config{
		Address: cfg.DNS.Address, Handler: handler.Handle, ReadTimeout: cfg.DNS.ReadTimeout,
		WriteTimeout: cfg.DNS.WriteTimeout, HandlerTimeout: cfg.DNS.HandlerTimeout, Concurrency: cfg.DNS.Concurrency,
	}}
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
	shutdownCtx, stop := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer stop()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	return errors.Join(first, <-results, shutdownErr)
}
