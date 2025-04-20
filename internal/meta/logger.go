package meta

import (
	"context"

	"github.com/phuslu/log"
)

type LoggerKey struct{}

func WithLogger(ctx context.Context, logger *log.Logger) context.Context {
	return context.WithValue(ctx, LoggerKey{}, logger)
}

func GetLogger(ctx context.Context) *log.Logger {
	return ctx.Value(LoggerKey{}).(*log.Logger)
}