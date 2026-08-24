package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/thefuriousowl/iot-edge/internal/auth"
	authpostgres "github.com/thefuriousowl/iot-edge/internal/auth/postgres"
	"github.com/thefuriousowl/iot-edge/internal/config"
)

const retainedPreviousPasswords = 2

type passwordService interface {
	ValidatePassword(username, password string) error
	HashPassword(password string) (string, error)
	VerifyPassword(password, passwordHash string) bool
}

func main() {
	username := flag.String("username", "", "username whose password will be reset")
	flag.Parse()
	if strings.TrimSpace(*username) == "" {
		log.Fatal("--username is required")
	}
	password, err := readPassword(os.Stdin)
	if err != nil {
		log.Fatalf("read new password from stdin: %v", err)
	}
	defer clear(password)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	database, err := config.ConnectDatabase(cfg)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		log.Fatalf("access database connection: %v", err)
	}
	defer sqlDatabase.Close()

	repository := authpostgres.NewUserRepository(database)
	service, err := auth.NewAuthService(repository, &auth.ServiceConfig{
		JWTSecret:        cfg.JWTSecret,
		JWTAccessExpiry:  cfg.JWTAccessExpiry,
		JWTRefreshExpiry: cfg.JWTRefreshExpiry,
	})
	if err != nil {
		log.Fatalf("initialize password policy: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := resetPassword(ctx, repository, service, strings.TrimSpace(*username), string(password)); err != nil {
		log.Fatalf("reset password: %v", err)
	}
	log.Printf("password reset completed for %q; existing sessions are invalidated", strings.TrimSpace(*username))
}

func readPassword(reader io.Reader) ([]byte, error) {
	buffered := bufio.NewReader(io.LimitReader(reader, 1025))
	password, err := buffered.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	password = []byte(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	if len(password) == 0 {
		return nil, errors.New("stdin contained an empty password")
	}
	if len(password) > 1024 {
		clear(password)
		return nil, errors.New("stdin password exceeds 1024 bytes")
	}
	return password, nil
}

func resetPassword(ctx context.Context, repository auth.UserRepository, service passwordService, username, password string) error {
	user, err := repository.FindByUsername(ctx, username)
	if err != nil {
		return err
	}
	if err := service.ValidatePassword(user.Username, password); err != nil {
		return err
	}
	if service.VerifyPassword(password, user.PasswordHash) {
		return auth.ErrPasswordRecentlyUsed
	}
	recentHashes, err := repository.RecentPasswordHashes(ctx, user.ID, retainedPreviousPasswords)
	if err != nil {
		return err
	}
	for _, passwordHash := range recentHashes {
		if service.VerifyPassword(password, passwordHash) {
			return auth.ErrPasswordRecentlyUsed
		}
	}
	passwordHash, err := service.HashPassword(password)
	if err != nil {
		return err
	}
	if err := repository.ChangePassword(ctx, user.ID, user.PasswordHash, passwordHash, retainedPreviousPasswords); err != nil {
		return fmt.Errorf("persist reset: %w", err)
	}
	return nil
}
