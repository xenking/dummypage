package usignal

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/phuslu/log"
)

const interruptMessage = "handle SIGINT, SIGTERM, SIGKILL, SIGQUIT signals"

var interruptSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGQUIT}

type DeferFunc func()

func InterruptContext() (context.Context, DeferFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	interrupt := make(chan os.Signal)
	signal.Notify(interrupt, interruptSignals...)
	deferFunc := func() {
		<-interrupt
		log.Info().Msg(interruptMessage)
		cancel()
	}
	return ctx, deferFunc
}
