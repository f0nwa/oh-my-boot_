package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pxe-platform/internal/images"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "add-path" {
		log.Fatal("usage: pxe-imagesctl add-path -name NAME -kind local|mounted -root ABSOLUTE_PATH")
	}
	flags := flag.NewFlagSet("add-path", flag.ExitOnError)
	name := flags.String("name", "", "storage name")
	kind := flags.String("kind", "local", "local or mounted")
	root := flags.String("root", "", "existing absolute directory")
	if err := flags.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
	if dsn := os.Getenv("PXE_IMAGES_DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
		storage, err := (images.Store{Pool: pool}).AddStorage(ctx, *name, *kind, *root)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("added storage %d: %s (%s)\n", storage.ID, storage.Name, storage.Kind)
		return
	}
	log.Fatal("PXE_IMAGES_DATABASE_URL is required")
}
