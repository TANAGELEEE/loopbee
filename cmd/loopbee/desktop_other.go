//go:build (!darwin && !windows) || nogui

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func desktop(url, data string, shutdown func()) error {
	fmt.Println("Running without a desktop window (nogui build or unsupported desktop platform).")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}
