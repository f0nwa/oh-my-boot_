package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pxe-platform/internal/boot"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	assets := flag.String("assets", "./assets", "boot asset directory")
	snapshot := flag.String("snapshot", "./menu.ipxe", "published menu snapshot")
	baseURL := flag.String("base-url", "", "client-visible HTTP base URL")
	flag.Parse()
	if *baseURL == "" {
		log.Fatal("-base-url is required")
	}
	menu, err := boot.LoadOrCreateSnapshot(*snapshot, *baseURL)
	if err != nil {
		log.Fatal(err)
	}
	var store boot.EventStore
	if dsn := os.Getenv("PXE_BOOT_DATABASE_URL"); dsn != "" {
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
		store = boot.PostgresStore{Pool: pool}
	}
	recorder := boot.NewRecorder(store, log.Default())
	handler, err := boot.NewHandler(*assets, menu, recorder.Record)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("PXE Boot listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("HTTP server failed: %v", err)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recorder.Close(drainCtx); err != nil {
		log.Printf("boot event drain: %v", err)
	}
}
