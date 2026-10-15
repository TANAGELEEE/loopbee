//go:build (!darwin && !windows) || nogui

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"loopbee/internal/core"
)

func desktop(url string, a *core.App, shutdown func()) error {
	a.EnableUpdates(nil)
	fmt.Println("Running without a desktop window (nogui build or unsupported desktop platform).")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}
