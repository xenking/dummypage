package main

import (
	"context"
	"github.com/xenking/dummypage/pkg/storage/memory"
	"os"
	"os/signal"
	"syscall"

	"github.com/phuslu/log"
	"github.com/xenking/dummypage/api/server"
	"github.com/xenking/dummypage/config"
	"github.com/xenking/dummypage/metrics"
)

const prefix = "APP"

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
	if err := initConfig(prefix, cfg); err != nil {
		log.Fatal().Err(err).Stack().Msg("init config")
	}
	cfg.Server.Limiter.Storage = memory.New()
	cfg.Server.Cache.Storage = memory.New()

	// Create global logger
	if err := initLogger(cfg.Log); err != nil {
		log.Fatal().Err(err).Stack().Msg("init logger")
	}

	s := server.New(cfg.Server)
	metric := metrics.New(cfg.Metrics)
	metric.RegisterAt(s.App)
	s.Run(ctx)

	return nil
}

func initConfig(prefix string, cfg *config.Config) error {
	err := config.LoadEnv(prefix, &cfg)
	if err != nil {
		return err
	}
	return nil
}

func initLogger(cfg config.LogConfig) error {
	log.DefaultLogger = log.Logger{
		Level:  log.ParseLevel(cfg.Level),
		Caller: 1,
		Writer: &log.MultiWriter{
			InfoWriter: &log.FileWriter{
				Filename: cfg.Filename + ".info.log",
				MaxSize:  cfg.FileMaxSize,
			},
			WarnWriter: &log.FileWriter{
				Filename: cfg.Filename + ".warn.log",
				MaxSize:  cfg.FileMaxSize,
			},
			ErrorWriter: &log.FileWriter{
				Filename: cfg.Filename + ".error.log",
				MaxSize:  cfg.FileMaxSize,
			},
			ConsoleWriter: &log.ConsoleWriter{
				ColorOutput:    true,
				EndWithMessage: true,
			},
			ConsoleLevel: log.ParseLevel(cfg.Level),
		},
	}
	return nil
}
