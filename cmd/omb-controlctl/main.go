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
	"omb/internal/control"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "create-admin" && os.Args[1] != "reset-password" && os.Args[1] != "reset-2fa") {
		log.Fatal("usage: omb-controlctl create-admin|reset-password|reset-2fa -username NAME [-language ru|en]")
	}
	cmd := os.Args[1]
	options := flag.NewFlagSet(cmd, flag.ExitOnError)
	username := options.String("username", "", "local username")
	language := options.String("language", "ru", "initial language for create-admin (ru or en)")
	if err := options.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
	if *username == "" {
		log.Fatal("username is required")
	}
	if cmd == "create-admin" && *language != "ru" && *language != "en" {
		log.Fatal("language must be ru or en")
	}
	dsn := os.Getenv("OMB_CONTROL_DATABASE_URL")
	if dsn == "" {
		log.Fatal("OMB_CONTROL_DATABASE_URL is required")
	}
	var password string
	if cmd != "reset-2fa" {
		fmt.Fprint(os.Stderr, "Новый пароль (из stdin): ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			log.Fatal("password must end with a newline")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	store := control.Store{Pool: pool}
	switch cmd {
	case "create-admin":
		ctx := context.Background()
		if err := store.CreateInitialAdmin(ctx, *username, password); err != nil {
			log.Fatal(err)
		}
		if *language == "en" {
			id, err := store.UserID(ctx, *username)
			if err != nil {
				log.Fatal(err)
			}
			if err := store.SetUserLanguage(ctx, id, "en"); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Fprintln(os.Stderr, "Администратор создан")
	case "reset-password":
		id, err := store.UserID(context.Background(), *username)
		if err != nil {
			log.Fatal(err)
		}
		if err := store.ResetPassword(context.Background(), id, password); err != nil {
			log.Fatal(err)
		}
		if err := store.AuditSystem(context.Background(), "user.reset-password", "ok", *username); err != nil {
			log.Printf("password reset succeeded, audit failed: %v", err)
		}
		fmt.Fprintln(os.Stderr, "Пароль изменён; прежние сеансы завершены")
	case "reset-2fa":
		id, err := store.UserID(context.Background(), *username)
		if err != nil {
			log.Fatal(err)
		}
		if err := store.ResetUserTOTP(context.Background(), id); err != nil {
			log.Fatal(err)
		}
		if err := store.AuditSystem(context.Background(), "user.reset_2fa", "ok", *username); err != nil {
			log.Printf("2fa reset succeeded, audit failed: %v", err)
		}
		fmt.Fprintln(os.Stderr, "2FA сброшена; прежние сеансы завершены")
	}
}
