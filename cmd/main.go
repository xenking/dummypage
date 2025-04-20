package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/phuslu/log"

	"github.com/xenking/dummypage/internal/config"
	"github.com/xenking/dummypage/internal/meta"
	"github.com/xenking/dummypage/internal/server"
)

func main() {
	ctx, cancel := appContext()
	defer cancel()

	if err := runMain(ctx); err != nil {
		log.Fatal().Err(err)
	}
}

// appContext returns context that will be cancelled on specific OS signals.
func appContext() (context.Context, context.CancelFunc) {
	signals := []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

	ctx, cancel := signal.NotifyContext(context.Background(), signals...)
	return ctx, cancel
}

func runMain(ctx context.Context) error {
	cfg := &config.Config{}

	// Load configuration from environment
	if err := initConfig(cfg); err != nil {
		log.Fatal().Err(err).Stack().Msg("init config")
	}

	// Create global logger
	if err := initLogger(cfg.Log); err != nil {
		log.Fatal().Err(err).Stack().Msg("init logger")
	}

	ctx = meta.WithLogger(ctx, &log.DefaultLogger)

	s := server.New(cfg.Server, &log.DefaultLogger)
	s.Run(ctx)

	return nil
}

func initConfig(cfg *config.Config) error {
	const envPrefix = "APP"

	err := config.LoadEnv(envPrefix, cfg)
	if err != nil {
		return err
	}
	return nil
}

func initLogger(cfg config.LogConfig) error {
	log.DefaultLogger = log.Logger{
		Level: log.ParseLevel(cfg.Level),
		Writer: &log.ConsoleWriter{
			ColorOutput:    true,
			EndWithMessage: true,
		},
	}
	return nil
}
