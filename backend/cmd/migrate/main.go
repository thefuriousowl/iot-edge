package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/thefuriousowl/iot-edge/internal/config"
	"github.com/thefuriousowl/iot-edge/internal/dbmaintenance"
	"github.com/thefuriousowl/iot-edge/migrations"
)

func main() {
	native := flag.Bool("native", false, "load the installed Windows DPAPI-protected configuration")
	flag.Parse()
	var databaseURL string
	if *native {
		cfg, err := config.LoadWindowsNative()
		if err != nil {
			log.Fatalf("load native configuration: %v", err)
		}
		databaseURL = cfg.DatabaseURL
	} else {
		databaseURL = os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			log.Fatal("DATABASE_URL is required")
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	if _, err := dbmaintenance.ValidateDedicated(ctx, database); err != nil {
		log.Fatalf("validate dedicated database: %v", err)
	}
	if err := migrations.Up(ctx, database); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	log.Print("database migrations are up to date")
}
