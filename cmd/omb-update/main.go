package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"omb/internal/update"
)

func main() {
	addr := net.ParseIP(os.Getenv("OMB_UPDATE_MANAGEMENT_IP"))
	if addr == nil || addr.To4() == nil {
		log.Fatal("OMB_UPDATE_MANAGEMENT_IP must be a management IPv4 address")
	}
	const publicKey = "/etc/omb/release-public.pem"
	info, err := os.Stat(publicKey)
	if err != nil || !info.Mode().IsRegular() {
		log.Fatal("configure /etc/omb/release-public.pem before starting omb-update")
	}
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		log.Fatal("omb-update requires socket activation")
	}
	file := os.NewFile(3, "omb-update.socket")
	listener, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		log.Fatal(err)
	}
	if _, ok := listener.(*net.UnixListener); !ok {
		log.Fatal("omb-update requires a Unix socket")
	}

	service, err := update.NewService("/var/lib/omb-update", update.LocalInstaller{
		Script:        "/usr/lib/omb/offline-update/offline-deploy.sh",
		PublicKey:     publicKey,
		Address:       addr.To4().String(),
		DeploymentDir: "/var/lib/omb-deployment",
	})
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Handler:           service,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Minute,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	running, err := os.Stat("/proc/self/exe")
	if err != nil {
		log.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-served:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
			return
		case <-ticker.C:
			installed, err := os.Stat("/usr/bin/omb-update")
			replaced := os.IsNotExist(err) || err == nil && !os.SameFile(running, installed)
			if replaced && service.BeginRestart() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := server.Shutdown(ctx)
				cancel()
				if err != nil {
					log.Printf("shut down replaced updater: %v", err)
				}
				return
			}
		}
	}
}
