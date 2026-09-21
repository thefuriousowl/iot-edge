package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/config"
	"github.com/thefuriousowl/iot-edge/internal/dbmaintenance"
	"github.com/thefuriousowl/iot-edge/internal/winruntime"
)

func main() {
	pgDump := flag.String("pg-dump", "", "absolute path to pg_dump.exe")
	output := flag.String("output", "", "absolute .dump path beneath the IoT Edge backup directory")
	flag.Parse()
	paths, err := winruntime.DefaultPaths()
	if err != nil {
		log.Fatalf("resolve native paths: %v", err)
	}
	if *output == "" {
		*output = filepath.Join(paths.BackupDir, "iot-edge-"+time.Now().UTC().Format("20060102T150405Z")+".dump")
	}
	if !dbmaintenance.Within(*output, paths.BackupDir) {
		log.Fatal("backup output must remain beneath the IoT Edge backup directory")
	}
	cfg, err := config.LoadNative(paths)
	if err != nil {
		log.Fatalf("load native configuration: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	database, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()
	target, err := dbmaintenance.ValidateDedicated(ctx, database)
	if err != nil {
		log.Fatalf("validate dedicated database: %v", err)
	}
	metadata, err := dbmaintenance.Backup(ctx, cfg.DatabaseURL, *pgDump, filepath.Clean(*output), target.Database)
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
	log.Printf("backup completed: %s (sha256 %s)", filepath.Base(*output), metadata.SHA256)
}
