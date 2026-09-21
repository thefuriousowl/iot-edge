package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
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
	input := flag.String("input", "", "absolute backup .dump path beneath the IoT Edge backup directory")
	pgDump := flag.String("pg-dump", "", "absolute path to pg_dump.exe")
	pgRestore := flag.String("pg-restore", "", "absolute path to pg_restore.exe")
	flag.Parse()
	paths, err := winruntime.DefaultPaths()
	if err != nil {
		log.Fatalf("resolve native paths: %v", err)
	}
	if *input == "" || !dbmaintenance.Within(*input, paths.BackupDir) {
		log.Fatal("restore input must remain beneath the IoT Edge backup directory")
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
	phrase := "RESTORE " + target.Database
	fmt.Fprintf(os.Stderr, "Type %s and press Enter to replace the dedicated database: ", phrase)
	if err := dbmaintenance.RequireConfirmation(os.Stdin, phrase); err != nil {
		log.Fatalf("confirmation rejected: %v", err)
	}
	rollback := filepath.Join(paths.BackupDir, "pre-restore-"+time.Now().UTC().Format("20060102T150405Z")+".dump")
	if _, err := dbmaintenance.Backup(ctx, cfg.DatabaseURL, *pgDump, rollback, target.Database); err != nil {
		log.Fatalf("create rollback backup: %v", err)
	}
	if err := dbmaintenance.Restore(ctx, cfg.DatabaseURL, *pgRestore, filepath.Clean(*input), target.Database); err != nil {
		rollbackErr := dbmaintenance.Restore(context.WithoutCancel(ctx), cfg.DatabaseURL, *pgRestore, rollback, target.Database)
		if rollbackErr != nil {
			log.Fatal(errors.Join(fmt.Errorf("restore failed: %w", err), fmt.Errorf("rollback failed: %w", rollbackErr)))
		}
		log.Fatalf("restore failed and the pre-restore backup was reapplied: %v", err)
	}
	log.Printf("restore completed; rollback backup retained as %s", filepath.Base(rollback))
}
