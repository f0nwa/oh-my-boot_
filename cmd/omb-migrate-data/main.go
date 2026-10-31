package main

import (
	"context"
	"omb/internal/ombmigration"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(ombmigration.RunCLI(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
