package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pxe-platform/internal/control"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "create-admin" {
		log.Fatal("usage: pxe-controlctl create-admin -username NAME (password from stdin)")
	}
	options := flag.NewFlagSet("create-admin", flag.ExitOnError)
	username := options.String("username", "", "initial administrator username")
	if err := options.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
	if *username == "" {
		log.Fatal("username is required")
	}
	dsn := os.Getenv("PXE_CONTROL_DATABASE_URL")
	if dsn == "" {
		log.Fatal("PXE_CONTROL_DATABASE_URL is required")
	}
	fmt.Fprint(os.Stderr, "Пароль первого администратора (из stdin): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		log.Fatal("password must end with a newline")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	store := control.Store{Pool: pool}
	if err := store.CreateInitialAdmin(context.Background(), *username, password); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "Администратор создан")
}
