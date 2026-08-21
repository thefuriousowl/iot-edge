package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserRepository_Integration(t *testing.T) {
	repository := newTestUserRepository(t)
	ctx := context.Background()

	count, err := repository.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() before create: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountUsers() before create = %d, want 0", count)
	}

	user := &domain.User{
		Username:     "admin",
		PasswordHash: "current-test-password-hash",
	}
	if err := repository.Create(ctx, user); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if user.ID == uuid.Nil {
		t.Fatal("Create() left user ID empty")
	}

	count, err = repository.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() after create: %v", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() after create = %d, want 1", count)
	}

	foundByID, err := repository.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindByID() error: %v", err)
	}
	if foundByID.Username != user.Username || foundByID.PasswordHash != user.PasswordHash {
		t.Errorf("FindByID() = %#v, want username and password hash from created user", foundByID)
	}

	foundByUsername, err := repository.FindByUsername(ctx, user.Username)
	if err != nil {
		t.Fatalf("FindByUsername() error: %v", err)
	}
	if foundByUsername.ID != user.ID {
		t.Errorf("FindByUsername() ID = %s, want %s", foundByUsername.ID, user.ID)
	}

	if _, err := repository.FindByID(ctx, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("FindByID() missing error = %v, want ErrUserNotFound", err)
	}
	if _, err := repository.FindByUsername(ctx, "missing-user"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("FindByUsername() missing error = %v, want ErrUserNotFound", err)
	}

	lockedUntil := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Microsecond)
	lastLogin := time.Now().UTC().Truncate(time.Microsecond)
	user.IsLocked = true
	user.FailedAttempts = 5
	user.LockedUntil = &lockedUntil
	user.LastLogin = &lastLogin
	if err := repository.Update(ctx, user); err != nil {
		t.Fatalf("Update() locked state error: %v", err)
	}

	updated, err := repository.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindByID() after update: %v", err)
	}
	if !updated.IsLocked || updated.FailedAttempts != 5 || updated.LockedUntil == nil || updated.LastLogin == nil {
		t.Errorf("updated auth state = %#v, want locked state and login timestamps", updated)
	}

	user.IsLocked = false
	user.FailedAttempts = 0
	user.LockedUntil = nil
	if err := repository.Update(ctx, user); err != nil {
		t.Fatalf("Update() zero values error: %v", err)
	}
	updated, err = repository.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindByID() after zero-value update: %v", err)
	}
	if updated.IsLocked || updated.FailedAttempts != 0 || updated.LockedUntil != nil {
		t.Errorf("zero-value auth state = %#v, want unlocked state", updated)
	}

	missingUser := *user
	missingUser.ID = uuid.New()
	if err := repository.Update(ctx, &missingUser); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Update() missing error = %v, want ErrUserNotFound", err)
	}

	for _, hash := range []string{"oldest-hash", "middle-hash", "newest-hash"} {
		if err := repository.AddPasswordHistory(ctx, user.ID, hash); err != nil {
			t.Fatalf("AddPasswordHistory(%q) error: %v", hash, err)
		}
		time.Sleep(time.Millisecond)
	}
	hashes, err := repository.RecentPasswordHashes(ctx, user.ID, 2)
	if err != nil {
		t.Fatalf("RecentPasswordHashes() error: %v", err)
	}
	wantHashes := []string{"newest-hash", "middle-hash"}
	if len(hashes) != len(wantHashes) {
		t.Fatalf("RecentPasswordHashes() = %#v, want %#v", hashes, wantHashes)
	}
	for i := range wantHashes {
		if hashes[i] != wantHashes[i] {
			t.Errorf("RecentPasswordHashes()[%d] = %q, want %q", i, hashes[i], wantHashes[i])
		}
	}
	emptyHashes, err := repository.RecentPasswordHashes(ctx, user.ID, 0)
	if err != nil {
		t.Fatalf("RecentPasswordHashes(limit=0) error: %v", err)
	}
	if len(emptyHashes) != 0 {
		t.Errorf("RecentPasswordHashes(limit=0) = %#v, want empty", emptyHashes)
	}

	revoked, err := repository.IsTokenRevoked(ctx, "refresh-token-id")
	if err != nil {
		t.Fatalf("IsTokenRevoked() before revoke: %v", err)
	}
	if revoked {
		t.Fatal("IsTokenRevoked() before revoke = true, want false")
	}
	if err := repository.RevokeToken(ctx, "refresh-token-id", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RevokeToken() error: %v", err)
	}
	revoked, err = repository.IsTokenRevoked(ctx, "refresh-token-id")
	if err != nil {
		t.Fatalf("IsTokenRevoked() after revoke: %v", err)
	}
	if !revoked {
		t.Fatal("IsTokenRevoked() after revoke = false, want true")
	}
}

func TestUserRepository_CreateInitialUser_Integration(t *testing.T) {
	repository := newTestUserRepository(t)
	ctx := context.Background()

	firstUser := &domain.User{
		Username:     "initial-admin",
		PasswordHash: "first-password-hash",
	}
	if err := repository.CreateInitialUser(ctx, firstUser); err != nil {
		t.Fatalf("CreateInitialUser() first call error: %v", err)
	}
	if firstUser.ID == uuid.Nil {
		t.Fatal("CreateInitialUser() left first user ID empty")
	}

	secondUser := &domain.User{
		Username:     "second-admin",
		PasswordHash: "second-password-hash",
	}
	if err := repository.CreateInitialUser(ctx, secondUser); !errors.Is(err, ErrInitialUserExists) {
		t.Fatalf("CreateInitialUser() second call error = %v, want ErrInitialUserExists", err)
	}
	if secondUser.ID != uuid.Nil {
		t.Errorf("CreateInitialUser() assigned rejected user ID %s, want empty", secondUser.ID)
	}

	count, err := repository.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() after CreateInitialUser calls: %v", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() after CreateInitialUser calls = %d, want 1", count)
	}
}

func TestUserRepository_CreateInitialUserConcurrent_Integration(t *testing.T) {
	repository := newTestUserRepository(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	users := []*domain.User{
		{Username: "concurrent-admin-one", PasswordHash: "password-hash-one"},
		{Username: "concurrent-admin-two", PasswordHash: "password-hash-two"},
	}
	type createResult struct {
		user *domain.User
		err  error
	}

	start := make(chan struct{})
	results := make(chan createResult, len(users))
	var workers sync.WaitGroup
	for _, user := range users {
		workers.Add(1)
		go func(user *domain.User) {
			defer workers.Done()
			<-start
			results <- createResult{
				user: user,
				err:  repository.CreateInitialUser(ctx, user),
			}
		}(user)
	}

	close(start)
	workers.Wait()
	close(results)

	successes := 0
	alreadyExistsErrors := 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			if result.user.ID == uuid.Nil {
				t.Error("successful CreateInitialUser() left user ID empty")
			}
		case errors.Is(result.err, ErrInitialUserExists):
			alreadyExistsErrors++
		default:
			t.Errorf("CreateInitialUser() concurrent unexpected error: %v", result.err)
		}
	}

	if successes != 1 {
		t.Errorf("concurrent CreateInitialUser() successes = %d, want 1", successes)
	}
	if alreadyExistsErrors != 1 {
		t.Errorf("concurrent CreateInitialUser() ErrInitialUserExists count = %d, want 1", alreadyExistsErrors)
	}

	count, err := repository.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() after concurrent creates: %v", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() after concurrent creates = %d, want 1", count)
	}
}

func newTestUserRepository(t *testing.T) UserRepository {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL repository integration test")
	}

	ctx := context.Background()
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := adminDB.Close(); err != nil {
			t.Errorf("closing PostgreSQL connection: %v", err)
		}
	})

	schemaName := "repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("creating isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("dropping isolated test schema: %v", err)
		}
	})

	testDatabaseURL, err := databaseURLWithSearchPath(databaseURL, schemaName)
	if err != nil {
		t.Fatalf("adding test schema to database URL: %v", err)
	}
	db, err := gorm.Open(postgres.Open(testDatabaseURL), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening isolated GORM connection: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("getting isolated SQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("closing isolated GORM connection: %v", err)
		}
	})

	migration, err := os.ReadFile("../../migrations/000001_create_users.up.sql")
	if err != nil {
		t.Fatalf("reading users migration: %v", err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("applying users migration: %v", err)
	}

	return NewUserRepository(db)
}

func databaseURLWithSearchPath(databaseURL, schemaName string) (string, error) {
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	query := parsedURL.Query()
	query.Set("search_path", schemaName)
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}
