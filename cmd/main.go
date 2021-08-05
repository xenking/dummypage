package main

import (
	"github.com/phuslu/log"
	"github.com/xenking/dummypage/api/server"
	"github.com/xenking/dummypage/config"
	"github.com/xenking/dummypage/metrics"
	"github.com/xenking/dummypage/usignal"
	"runtime"
)

const prefix = "APP"

func main() {
	// Completely disable memory profiling if we aren't going to use it.
	// If we don't do this the profiler will take a sample every 0.5MiB bytes allocated.
	runtime.MemProfileRate = 0
	runtime.GOMAXPROCS(runtime.NumCPU())

	cfg := &config.Config{}

	ctx, cancel := usignal.InterruptContext()

	// Load configuration from environment
	if err := initConfig(prefix, cfg); err != nil {
		log.Fatal().Err(err).Stack().Msg("init config")
	}

	// Create global logger
	if err := initLogger(cfg); err != nil {
		log.Fatal().Err(err).Stack().Msg("init logger")
	}
	s := server.New(cfg.API.Server)
	metric := metrics.New(cfg.Metrics)
	metric.RegisterAt(s.App)
	s.Run(ctx)
	cancel()
}

func initConfig(prefix string, cfg *config.Config) error {
	err := config.LoadEnv(prefix, &cfg)
	if err != nil {
		return err
	}
	return nil
}

func initLogger(cfg *config.Config) error {
	log.DefaultLogger = log.Logger{
		Level:  log.ParseLevel(cfg.Log.Level),
		Caller: 1,
		Writer: &log.MultiWriter{
			InfoWriter: &log.FileWriter{
				Filename: cfg.Log.Filename + ".info.log",
				MaxSize:  cfg.Log.FileMaxSize,
			},
			WarnWriter: &log.FileWriter{
				Filename: cfg.Log.Filename + ".warn.log",
				MaxSize:  cfg.Log.FileMaxSize,
			},
			ErrorWriter: &log.FileWriter{
				Filename: cfg.Log.Filename + ".error.log",
				MaxSize:  cfg.Log.FileMaxSize,
			},
			ConsoleWriter: &log.ConsoleWriter{
				ColorOutput:    true,
				EndWithMessage: true,
			},
			ConsoleLevel: log.ParseLevel(cfg.Log.Level),
		},
	}
	return nil
}
