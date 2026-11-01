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
	"omb/internal/boot"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	assets := flag.String("assets", "./assets", "boot asset directory")
	snapshot := flag.String("snapshot", "./menu.ipxe", "published menu snapshot")
	baseURL := flag.String("base-url", "", "client-visible HTTP base URL")
	controlSocket := flag.String("control-socket", "/run/omb-boot/control.sock", "local control socket")
	flag.Parse()
	if *baseURL == "" {
		log.Fatal("-base-url is required")
	}
	menu, err := boot.LoadOrCreateSnapshot(*snapshot, *baseURL)
	if err != nil {
		log.Fatal(err)
	}
	publisher, err := boot.NewPublisher(*snapshot, menu)
	if err != nil {
		log.Fatal(err)
	}
	var store boot.EventStore
	var reader boot.EventReader
	journal := &boot.Journal{Path: filepath.Join(filepath.Dir(*snapshot), "journal.json")}
	if dsn := os.Getenv("OMB_BOOT_DATABASE_URL"); dsn != "" {
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
		store = boot.PostgresStore{Pool: pool}
		reader = boot.PostgresStore{Pool: pool}
		journal.Store = boot.PostgresStore{Pool: pool}
	}
	recorder := boot.NewRecorder(store, log.Default())
	transfers := boot.NewTransfers()
	handler, err := boot.NewDynamicHandlerWithTransfers(*assets, publisher, recorder.Record, os.Getenv("OMB_BOOT_UBUNTU_ISO"), transfers)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if err := os.MkdirAll(filepath.Dir(*controlSocket), 0750); err != nil {
		log.Fatal(err)
	}
	if info, err := os.Lstat(*controlSocket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			log.Fatalf("refusing to replace non-socket %s", *controlSocket)
		}
		if err := os.Remove(*controlSocket); err != nil {
			log.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", *controlSocket)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(*controlSocket, 0660); err != nil {
		log.Fatal(err)
	}
	autoinstallAssets, err := boot.NewAutoinstallAssets(*assets)
	if err != nil {
		log.Fatal(err)
	}
	controlServer := &http.Server{Handler: boot.ControlHandlerWithUbuntuSource(publisher, reader, transfers, journal, autoinstallAssets, boot.NewUbuntuSource(os.Getenv("OMB_BOOT_UBUNTU_ISO"), *assets)), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = controlServer.Shutdown(shutdownCtx)
	}()
	if journal.Store != nil {
		go journal.Run(ctx)
	}
	go func() {
		if err := controlServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Boot control API failed: %v", err)
		}
	}()
	log.Printf("Oh My Boot: Boot listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("HTTP server failed: %v", err)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recorder.Close(drainCtx); err != nil {
		log.Printf("boot event drain: %v", err)
	}
}
