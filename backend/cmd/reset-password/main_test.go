package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/auth"
)

func TestReadPassword(t *testing.T) {
	password, err := readPassword(strings.NewReader("FreshP@ss3\nignored"))
	if err != nil {
		t.Fatalf("readPassword() error = %v", err)
	}
	if string(password) != "FreshP@ss3" {
		t.Fatalf("readPassword() = %q", password)
	}
	clear(password)

	if _, err := readPassword(strings.NewReader("\n")); err == nil {
		t.Fatal("readPassword() accepted an empty password")
	}
}

func TestResetPasswordValidatesHistoryAndInvalidatesSessions(t *testing.T) {
	repository := &resetRepository{
		user:   &auth.User{ID: uuid.New(), Username: "iot-admin", PasswordHash: "current-hash"},
		hashes: []string{"previous-hash"},
	}
	service := &resetService{}
	if err := resetPassword(context.Background(), repository, service, "iot-admin", "FreshP@ss3"); err != nil {
		t.Fatalf("resetPassword() error = %v", err)
	}
	if repository.receivedExpected != "current-hash" || repository.receivedHash != "hash:FreshP@ss3" || repository.receivedLimit != retainedPreviousPasswords {
		t.Fatalf("change request = expected %q hash %q limit %d", repository.receivedExpected, repository.receivedHash, repository.receivedLimit)
	}
	if service.validatedUsername != "iot-admin" || service.validatedPassword != "FreshP@ss3" {
		t.Fatalf("validated = %q/%q", service.validatedUsername, service.validatedPassword)
	}
}

func TestResetPasswordRejectsCurrentAndRecentPasswords(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{name: "current", password: "current-hash"},
		{name: "recent", password: "previous-hash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &resetRepository{
				user:   &auth.User{ID: uuid.New(), Username: "iot-admin", PasswordHash: "current-hash"},
				hashes: []string{"previous-hash"},
			}
			service := &resetService{plainHashes: true}
			if err := resetPassword(context.Background(), repository, service, "iot-admin", test.password); !errors.Is(err, auth.ErrPasswordRecentlyUsed) {
				t.Fatalf("resetPassword() error = %v", err)
			}
			if repository.changeCalls != 0 {
				t.Fatalf("ChangePassword() calls = %d", repository.changeCalls)
			}
		})
	}
}

type resetRepository struct {
	user             *auth.User
	hashes           []string
	receivedExpected string
	receivedHash     string
	receivedLimit    int
	changeCalls      int
}

func (repository *resetRepository) CountUsers(context.Context) (int64, error) { return 1, nil }
func (repository *resetRepository) Create(context.Context, *auth.User) error  { return nil }
func (repository *resetRepository) FindByID(context.Context, uuid.UUID) (*auth.User, error) {
	return repository.user, nil
}
func (repository *resetRepository) FindByUsername(context.Context, string) (*auth.User, error) {
	return repository.user, nil
}
func (repository *resetRepository) Update(context.Context, *auth.User) error { return nil }
func (repository *resetRepository) CreateInitialUser(context.Context, *auth.User) error {
	return nil
}
func (repository *resetRepository) AddPasswordHistory(context.Context, uuid.UUID, string) error {
	return nil
}
func (repository *resetRepository) RecentPasswordHashes(context.Context, uuid.UUID, int) ([]string, error) {
	return repository.hashes, nil
}
func (repository *resetRepository) ChangePassword(_ context.Context, _ uuid.UUID, expectedHash, newHash string, limit int) error {
	repository.changeCalls++
	repository.receivedExpected = expectedHash
	repository.receivedHash = newHash
	repository.receivedLimit = limit
	return nil
}
func (repository *resetRepository) RevokeToken(context.Context, string, uuid.UUID, time.Time) error {
	return nil
}
func (repository *resetRepository) IsTokenRevoked(context.Context, string) (bool, error) {
	return false, nil
}

type resetService struct {
	plainHashes       bool
	validatedUsername string
	validatedPassword string
}

func (service *resetService) ValidatePassword(username, password string) error {
	service.validatedUsername = username
	service.validatedPassword = password
	return nil
}
func (service *resetService) HashPassword(password string) (string, error) {
	return "hash:" + password, nil
}
func (service *resetService) VerifyPassword(password, passwordHash string) bool {
	return service.plainHashes && password == passwordHash
}
