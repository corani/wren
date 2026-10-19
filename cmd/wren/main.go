package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/spf13/cobra"

	"github.com/corani/wren"
	"github.com/corani/wren/internal/config"
	"github.com/corani/wren/internal/server"
	"github.com/corani/wren/internal/store"
)

var version = "dev"

func main() {
	if err := execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wren: %v\n", err)
		os.Exit(1)
	}
}

func execute() error {
	cfg, err := config.Default()
	if err != nil {
		return err
	}
	command := &cobra.Command{
		Use: "wren", Short: "A little Chinese, daily.", Version: version,
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), cfg)
		},
	}
	command.Flags().StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address (trusted networks only; no authentication)")
	command.Flags().StringVar(&cfg.Database, "db", cfg.Database, "SQLite database path")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return command.ExecuteContext(ctx)
}

func run(ctx context.Context, cfg config.Config) (result error) {
	log := slog.New(charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		Level: charmlog.InfoLevel, ReportTimestamp: true,
	}))
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, st.Close()) }()
	syncResult, err := st.Sync(ctx, wren.Files)
	if err != nil {
		return fmt.Errorf("sync vocabulary: %w", err)
	}
	log.Info("vocabulary ready", "terms", syncResult.Terms, "unavailable", syncResult.Unavailable,
		"changed_files", syncResult.ChangedFiles)
	assets, err := fs.Sub(wren.Files, "web")
	if err != nil {
		return fmt.Errorf("load assets: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	httpServer := &http.Server{
		Handler:           server.New(st, log, assets).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	serveError := make(chan error, 1)
	go func() { serveError <- httpServer.Serve(listener) }()
	log.Info("wren listening", "url", "http://"+listener.Addr().String(), "database", cfg.Database)
	select {
	case err := <-serveError:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdown); err != nil {
		return errors.Join(fmt.Errorf("shutdown: %w", err), httpServer.Close())
	}
	return nil
}
