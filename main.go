// hyrox-radar watches HYROX ticket shops for sold-out tickets coming back, and sends a Pushover
// notification the moment one can be bought. Its settings page picks the event from HYROX's
// Find my race and the tickets from the event's shop.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var version = "1.1.0"

func main() {
	home, _ := os.UserHomeDir()
	listen := flag.String("listen", ":8590", "address of the settings page")
	data := flag.String("data", filepath.Join(home, ".hyrox-radar"), "where the watches and settings are kept")
	chromium := flag.String("chromium", "", "Chromium or Chrome to use (default: found on the PATH)")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Parse()
	if *showVersion {
		log.SetFlags(0)
		log.Print(version)
		return
	}

	store, err := OpenStore(*data)
	if err != nil {
		log.Fatalf("opening %s: %v", *data, err)
	}
	shop := &Shop{Chromium: findChromium(*chromium)}
	events := &Events{}
	radar := NewRadar(store, shop, events)
	server := &Server{store: store, radar: radar, shop: shop, events: events}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go radar.Run(ctx)

	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	log.Printf("hyrox-radar %s: settings page on %s, data in %s", version, *listen, *data)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// findChromium is the given browser, or the first Chromium or Chrome on the PATH.
func findChromium(given string) string {
	if given != "" {
		return given
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return "chromium"
}
