package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"pinpoint/internal/store"
	"pinpoint/internal/web"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	dbPath := flag.String("db", "pinpoint.db", "path to the SQLite database file")
	flag.Parse()

	// Read before the database is open, as a typo here stops the server.
	proxies, err := web.ParseTrustedProxies(os.Getenv("PINPOINT_TRUSTED_PROXIES"))
	if err != nil {
		log.Fatal("PINPOINT_TRUSTED_PROXIES: ", err)
	}

	_, statErr := os.Stat(*dbPath)
	existed := statErr == nil

	s, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close() // only after serve has returned, so no request is still using it
	if existed {
		log.Printf("database: %s", *dbPath)
	} else {
		log.Print("database: created a new, empty one at " + *dbPath + `

    If this is not the first time this server has started, its reviews are not
    where it is looking, and every review link made before now will lead
    nowhere. Keep the database on storage that outlasts a restart: with Docker,
    a volume for /data (docker run -v pinpoint:/data ...).
`)
	}

	var opts []web.Option
	if on(os.Getenv("PINPOINT_ALLOW_PRIVATE")) {
		opts = append(opts, web.WithPrivateNetworks())
		log.Print("private networks: ALLOWED. Reviews can show sites on this machine and its network, so nobody else must be able to reach this server.")
	} else {
		log.Print("private networks: refused (reviews show public websites only)")
	}
	if len(proxies) > 0 {
		opts = append(opts, web.WithTrustedProxies(proxies))
		names := make([]string, len(proxies))
		for i, p := range proxies {
			names[i] = p.String()
		}
		log.Printf("trusted proxies: %s. Their X-Forwarded-For says who each visitor is, for the rate limits.", strings.Join(names, ", "))
	} else {
		log.Print("trusted proxies: none (rate limits count the address that connects, and X-Forwarded-For is ignored)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Handler:           web.New(s, opts...),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on http://%s (db: %s)", *addr, *dbPath)
	if err := serve(ctx, srv, ln); err != nil {
		log.Print(err)
		return // through the deferred Close, which log.Fatal would skip
	}
	log.Print("stopped")
}

func on(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b || strings.EqualFold(strings.TrimSpace(v), "on") || strings.EqualFold(strings.TrimSpace(v), "yes")
}

// serve runs srv on ln until ctx is cancelled, then shuts it down and returns
// only once the requests already in progress have finished, or ten seconds
// have passed.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	drained := make(chan error, 1)
	go func() {
		<-ctx.Done()
		log.Print("shutting down")
		quit, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		drained <- srv.Shutdown(quit)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err := <-drained; err != nil {
		log.Printf("some requests were cut short: %v", err)
	}
	return nil
}
