package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pxe-platform/internal/images"
)

func main() {
	socket := flag.String("socket", "/run/pxe-platform-images/images.sock", "local Images API socket")
	checkInterval := flag.Duration("storage-check-interval", time.Minute, "background storage availability check interval; 0 disables it")
	flag.Parse()
	dsn := os.Getenv("PXE_IMAGES_DATABASE_URL")
	if dsn == "" {
		log.Fatal("PXE_IMAGES_DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		log.Fatal(err)
	}
	store := images.Store{Pool: pool}
	if err := store.RecoverInterruptedScans(pingCtx); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*socket), 0750); err != nil {
		log.Fatal(err)
	}
	if info, err := os.Lstat(*socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			log.Fatalf("refusing to replace non-socket %s", *socket)
		}
		if err := os.Remove(*socket); err != nil {
			log.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(*socket, 0660); err != nil {
		log.Fatal(err)
	}
	api := images.NewServer(store)
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *checkInterval > 0 {
		monitor := &images.StorageMonitor{
			Repo:          store,
			Probe:         images.ProbeStorage,
			Rescan:        func(id int64) error { return api.StartScan(context.Background(), id) },
			Timeout:       20 * time.Second,
			RescanBackoff: 10 * time.Minute,
		}
		go monitor.Run(shutdownCtx, *checkInterval)
	}
	go func() {
		<-shutdownCtx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(timeout)
	}()
	log.Printf("PXE Images local API listening on %s", *socket)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
