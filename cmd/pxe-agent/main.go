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

	"pxe-platform/internal/agent"
)

func main() {
	socket := flag.String("socket", "/run/pxe-platform/agent.sock", "local control socket")
	config := flag.String("config-path", "/etc/pxe-platform/dnsmasq.conf", "managed dnsmasq config")
	state := flag.String("state-path", "/var/lib/pxe-platform-agent/network.json", "last successful network config")
	flag.Parse()
	applier, err := agent.NewApplier(*config, *state, agent.SystemRunner{})
	if err != nil {
		log.Fatal(err)
	}
	listener, err := agent.ListenUnix(*socket)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: agent.Handler(applier), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("PXE Network Agent listening on %s", *socket)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
