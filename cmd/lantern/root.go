package main

import (
	"context"
	"log"
	"time"

	"github.com/koreanmelon/lantern/pkg/dns"
	"github.com/spf13/cobra"
)

func newRootCommand(serve func(context.Context, string) error) *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:           "lantern",
		Short:         "Run Lantern's local DNS server",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log.New(cmd.ErrOrStderr(), "", log.LstdFlags).Printf("Starting DNS server on %s", address)
			return serve(cmd.Context(), address)
		},
	}
	cmd.Flags().StringVar(&address, "dns-listen", "127.0.0.1:1053", "DNS UDP and TCP listen address")
	return cmd
}

func serveDNS(ctx context.Context, address string) error {
	handler, err := exampleHandler()
	if err != nil {
		return err
	}
	server := dns.Server{
		Handler:       handler,
		Timeout:       10 * time.Second,
		MaxConcurrent: 10,
	}
	return server.ListenAndServe(ctx, address)
}
