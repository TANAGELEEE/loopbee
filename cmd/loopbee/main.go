package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"loopbee/internal/core"
	"loopbee/internal/httpapi"
	"loopbee/internal/update"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		if headless := flag.Lookup("nogui"); headless == nil || headless.Value.String() != "true" {
			showStartupError(err)
		}
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	defaultData := filepath.Join(home, "loopbee")
	if v := os.Getenv("LOOPBEE_DATA_DIR"); v != "" {
		defaultData = v
	}
	dir := flag.String("data", defaultData, "application data directory (or LOOPBEE_DATA_DIR)")
	listen := flag.String("listen", "127.0.0.1:0", "loopback HTTP address")
	headless := flag.Bool("nogui", false, "run the scheduler and API without a desktop window")
	flag.Parse()
	// Started by an update: the old loopbee may still hold the data folder for a moment.
	update.AwaitPredecessor()
	a, err := core.Open(*dir)
	if err != nil {
		return err
	}
	var closeOnce sync.Once
	closeCore := func() { closeOnce.Do(a.Close) }
	defer closeCore()
	listener, err := httpapi.Listen(*listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	api := httpapi.New(a, listener.Addr().String())
	server := httpapi.HTTPServer(api.Handler())
	url := "http://" + listener.Addr().String() + "/#token=" + api.Token
	if err = os.WriteFile(filepath.Join(a.Dir, "launch-url.txt"), []byte(url), 0600); err != nil {
		return err
	}
	fmt.Printf("%s listening on http://%s\nOpen the authenticated URL in %s\n", core.Brand, listener.Addr(), filepath.Join(*dir, "launch-url.txt"))
	a.ServeBackground()
	var stopOnce sync.Once
	shutdown := func() {
		stopOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(ctx)
			closeCore()
		})
	}
	defer shutdown()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
	}()
	if !*headless {
		return desktop(url, a, shutdown)
	} else {
		a.EnableUpdates(nil) // headless: learns about new versions, never replaces itself
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		<-ctx.Done()
		stop()
	}
	return nil
}
