package main

import (
	"context"
	"crypto/tls"
	"errors"
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
	secretsKey := flag.String("secrets-key", "/etc/omb/secrets.key", "32-byte key file for integration secrets")
	releaseInfo := flag.String("release-info", control.DefaultReleaseInfoPath, "path to release metadata JSON")
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
	var secretBox *control.SecretBox
	if *secretsKey != "" {
		box, err := control.LoadSecretBox(*secretsKey)
		if errors.Is(err, control.ErrSecretsKeyMissing) {
			hasEnc, checkErr := store.HasEncryptedIntegrations(pingCtx)
			if checkErr == nil && hasEnc {
				log.Fatalf("secrets key %s is missing, but encrypted integration settings exist in database", *secretsKey)
			}
		} else if err != nil {
			log.Fatalf("load secrets key: %v", err)
		} else {
			secretBox = box
		}
	}
	agentClient := control.NewAgentClient(*agentSocket)
	bootClient := control.NewBootClient(*bootSocket)
	var server *control.Server
	sftpServer := control.NewLocalSFTPServer(
		"/var/lib/omb-images/iso",
		"/var/lib/omb-control/sftp_host_ed25519_key",
		control.SFTPHooks{
			Authenticate: func(ctx context.Context, username, password string) (control.User, error) {
				return store.AuthenticateFSUser(ctx, username, password)
			},
			IsLocalMaintenance: func(ctx context.Context) bool {
				if server == nil {
					return false
				}
				return server.IsLocalMaintenance(ctx)
			},
			IsISOProtected: func(ctx context.Context, relPath string) bool {
				if server == nil {
					return false
				}
				return server.IsLocalISOProtected(ctx, relPath)
			},
			OnISOUploaded: func(ctx context.Context, user control.User, relPath string) {
				if server != nil {
					server.OnISOUploaded(ctx, user, relPath)
				}
			},
		},
	)
	server = &control.Server{
		Store:             store,
		Boot:              bootClient,
		UbuntuSource:      bootClient,
		DeletionMenu:      bootClient,
		Agent:             agentClient,
		DiskUsage:         agentClient,
		BootDrivers:       agentClient,
		SMB:               agentClient,
		NFS:               agentClient,
		WindowsISO:        agentClient,
		UbuntuISO:         agentClient,
		WindowsAnswers:    agentClient,
		DriverPackages:    agentClient,
		DriverConfigs:     agentClient,
		Cleanup:           agentClient,
		StorageRecovery:   agentClient,
		SystemTime:        agentClient,
		UEFICAUSB:         agentClient,
		LocalSMB:          agentClient,
		SFTPServer:        sftpServer,
		Secrets:           secretBox,
		SecretsKeyPath:    *secretsKey,
		ListenAddr:        *listen,
		Images:            control.NewImagesClient(*imagesSocket),
		AllowedInterfaces: allowlist,
		BootBaseURL:       os.Getenv("OMB_CONTROL_BOOT_BASE_URL"),
		ReleaseInfoPath:   *releaseInfo,
	}
	if err := server.SyncBootDrivers(pingCtx); err != nil {
		log.Printf("boot driver synchronization: %v", err)
	}
	if err := server.SyncBootAccess(pingCtx); err != nil {
		log.Printf("boot access synchronization: %v", err)
	}
	if err := server.SyncMaintenance(pingCtx); err != nil {
		log.Printf("maintenance synchronization: %v", err)
	}
	if err := server.SyncLocalFileServer(pingCtx); err != nil {
		log.Printf("local file server synchronization: %v", err)
	}
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if server.SysMon != nil {
		server.SysMon.Start(shutdownCtx)
	}
	control.NewSystemEventMonitor(server).Start(shutdownCtx)
	go func() {
		<-shutdownCtx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(timeout)
		if sftpServer != nil {
			_ = sftpServer.Close()
		}
	}()
	log.Printf("Oh My Boot: Control HTTPS listening on %s", *listen)
	if err := httpServer.ListenAndServeTLS(*cert, *key); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
