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
	if len(os.Args) < 2 || (os.Args[1] != "create-admin" && os.Args[1] != "reset-password") {
		log.Fatal("usage: omb-controlctl create-admin|reset-password -username NAME (password from stdin)")
	}
	options := flag.NewFlagSet("create-admin", flag.ExitOnError)
	username := options.String("username", "", "initial administrator username")
	if err := options.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
	if *username == "" {
		log.Fatal("username is required")
	}
	dsn := os.Getenv("OMB_CONTROL_DATABASE_URL")
	if dsn == "" {
		log.Fatal("OMB_CONTROL_DATABASE_URL is required")
	}
	fmt.Fprint(os.Stderr, "Новый пароль (из stdin): ")
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
	if os.Args[1]=="create-admin" {
		if err:=store.CreateInitialAdmin(context.Background(),*username,password);err!=nil{log.Fatal(err)}
		fmt.Fprintln(os.Stderr,"Администратор создан")
		return
	}
	id,err:=store.UserID(context.Background(),*username);if err!=nil{log.Fatal(err)}
	if err:=store.ResetPassword(context.Background(),id,password);err!=nil{log.Fatal(err)}
	if err:=store.AuditSystem(context.Background(),"user.reset-password","ok",*username);err!=nil{log.Printf("password reset succeeded, audit failed: %v",err)}
	fmt.Fprintln(os.Stderr,"Пароль изменён; прежние сеансы завершены")
}
