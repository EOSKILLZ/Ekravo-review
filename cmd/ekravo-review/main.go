package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/EOSKILLZ/ekravo-review/v2/internal/app"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/config"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/store"
	"github.com/EOSKILLZ/ekravo-review/v2/internal/version"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "validate configuration and exit")
		migrate     = flag.Bool("migrate", false, "import legacy .processed_prs.json and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("ekravo-review %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	logger := newLogger(cfg.Log)

	if *check {
		logger.Info("configuration valid",
			"repos", len(cfg.GitHub.Repos),
			"models", cfg.LLM.Models,
			"mode", mode(cfg),
			"database", cfg.Store.Path)
		return
	}

	if *migrate {
		st, err := store.Open(cfg.Store.Path)
		if err != nil {
			logger.Error("open database", "err", err)
			os.Exit(1)
		}
		defer st.Close()

		n, err := st.ImportLegacy(context.Background(), cfg.Store.LegacyStateFile)
		if err != nil {
			logger.Error("import legacy state", "err", err)
			os.Exit(1)
		}
		logger.Info("legacy state imported", "records", n, "source", cfg.Store.LegacyStateFile)
		return
	}

	a, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("initialise", "err", err)
		os.Exit(1)
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting Ekravo Review",
		"version", version.Version,
		"mode", mode(cfg),
		"models", cfg.LLM.Models,
		"workers", cfg.Bot.Workers,
		"repo_url", cfg.Bot.RepoURL)

	if err := a.Run(ctx); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
	logger.Info("stopped")
}

func mode(cfg *config.Config) string {
	if cfg.WebhookMode() {
		return "webhook"
	}
	return "polling"
}

func newLogger(c config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch c.Level {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN", "WARNING":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if c.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
