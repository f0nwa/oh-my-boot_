package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"omb/internal/control"
)

func main() {
	listen := flag.String("listen", "", "HTTPS management address")
	cert := flag.String("tls-cert", "", "TLS certificate with management IP SAN")
	key := flag.String("tls-key", "", "TLS private key")
	bootSocket := flag.String("boot-socket", "/run/omb-boot/control.sock", "Boot local API socket")
	agentSocket := flag.String("agent-socket", "/run/omb/agent.sock", "Agent local API socket")
	imagesSocket := flag.String("images-socket", "/run/omb-images/images.sock", "Images local API socket")
	allowed := flag.String("allowed-interfaces", "", "comma-separated PXE interface allowlist")
	flag.Parse()
	if *listen == "" || *cert == "" || *key == "" || *allowed == "" {
		log.Fatal("listen, TLS certificate/key and PXE interface allowlist are required")
	}
	dsn := os.Getenv("OMB_CONTROL_DATABASE_URL")
	if dsn == "" {
		log.Fatal("OMB_CONTROL_DATABASE_URL is required")
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
	allowlist := map[string]bool{}
	for _, part := range strings.Split(*allowed, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			allowlist[name] = true
		}
	}
	if len(allowlist) == 0 {
		log.Fatal("no allowed PXE interfaces")
	}
	store := control.Store{Pool: pool}
	agentClient := control.NewAgentClient(*agentSocket)
	bootClient := control.NewBootClient(*bootSocket)
	server := &control.Server{Store: store, Boot: bootClient, UbuntuSource: bootClient, DeletionMenu: bootClient, Agent: agentClient, DiskUsage: agentClient, BootDrivers: agentClient, SMB: agentClient, NFS: agentClient, WindowsISO: agentClient, UbuntuISO: agentClient, WindowsAnswers: agentClient, DriverPackages: agentClient, DriverConfigs: agentClient, Cleanup: agentClient, StorageRecovery: agentClient, SystemTime: agentClient, UEFICAUSB: agentClient, Images: control.NewImagesClient(*imagesSocket), AllowedInterfaces: allowlist, BootBaseURL: os.Getenv("OMB_CONTROL_BOOT_BASE_URL")}
	if err := server.SyncBootDrivers(pingCtx); err != nil {
		log.Printf("boot driver synchronization: %v", err)
	}
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if server.SysMon != nil {
		server.SysMon.Start(shutdownCtx)
	}
	go func() {
		<-shutdownCtx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(timeout)
	}()
	log.Printf("Oh My Boot: Control HTTPS listening on %s", *listen)
	if err := httpServer.ListenAndServeTLS(*cert, *key); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
