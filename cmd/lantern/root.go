package main

import (
	"context"
	"log"

	"github.com/koreanmelon/lantern/internal/config"
	"github.com/spf13/cobra"
)

func newRootCommand(serve func(context.Context, config.Config) error) *cobra.Command {
	var path string
	var settings config.Config
	defaults := config.Defaults()
	cmd := &cobra.Command{
		Use:           "lantern",
		Short:         "Run Lantern DNS and its web-based record manager",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			settings, err = config.Load(path, cmd.Flags())
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log.New(cmd.ErrOrStderr(), "", log.LstdFlags).Printf("Starting Lantern: DNS %s · web http://%s", settings.DNS.Address, settings.Web.Address)
			return serve(cmd.Context(), settings)
		},
	}
	cmd.Flags().String("dns-listen", defaults.DNS.Address, "DNS UDP and TCP listen address")
	cmd.Flags().String("web-listen", defaults.Web.Address, "Web record manager listen address (no authentication)")
	cmd.Flags().StringVar(&path, "config", "", "Explicit configuration file path (no automatic discovery)")
	return cmd
}
