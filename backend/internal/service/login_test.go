package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

type loginUserRepository struct {
	repository.UserRepository
	user             *domain.User
	findErr          error
	updateErr        error
	receivedCtx      context.Context
	receivedUsername string
	updatedUser      *domain.User
	findCalls        int
	updateCalls      int
}

func (r *loginUserRepository) FindByUsername(
	ctx context.Context,
	username string,
) (*domain.User, error) {
	r.findCalls++
	r.receivedCtx = ctx
	r.receivedUsername = username
	return r.user, r.findErr
}

func (r *loginUserRepository) Update(ctx context.Context, user *domain.User) error {
	r.updateCalls++
	r.receivedCtx = ctx
	r.updatedUser = user
	return r.updateErr
}

func TestLockDurationForFailedAttempts(t *testing.T) {
	tests := []struct {
		name           string
		failedAttempts int
		want           time.Duration
	}{
		{name: "negative", failedAttempts: -1, want: 0},
		{name: "zero", failedAttempts: 0, want: 0},
		{name: "below first threshold", failedAttempts: 4, want: 0},
		{name: "first threshold", failedAttempts: 5, want: 5 * time.Minute},
		{name: "between first and second thresholds", failedAttempts: 9, want: 5 * time.Minute},
		{name: "second threshold", failedAttempts: 10, want: 30 * time.Minute},
		{name: "between second and third thresholds", failedAttempts: 19, want: 30 * time.Minute},
		{name: "third threshold", failedAttempts: 20, want: 24 * time.Hour},
		{name: "above third threshold", failedAttempts: 21, want: 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lockDurationForFailedAttempts(tt.failedAttempts)
			if got != tt.want {
				t.Errorf(
					"lockDurationForFailedAttempts(%d) = %s, want %s",
					tt.failedAttempts,
					got,
					tt.want,
				)
			}
		})
	}
}

func TestLogin_MapsMissingUserToInvalidCredentials(t *testing.T) {
	users := &loginUserRepository{findErr: repository.ErrUserNotFound}
	auth := newLoginTestService(t, users, time.Now())

	tokens, err := auth.Login(context.Background(), "missing", "AnyPassword1!")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if tokens != nil {
		t.Errorf("Login() tokens = %#v, want nil", tokens)
	}
	if users.updateCalls != 0 {
		t.Errorf("Update() calls = %d, want 0", users.updateCalls)
	}
}

func TestLogin_ReturnsRepositoryLookupError(t *testing.T) {
	repositoryError := errors.New("test lookup error")
	users := &loginUserRepository{findErr: repositoryError}
	auth := newLoginTestService(t, users, time.Now())

	tokens, err := auth.Login(context.Background(), "admin", "AnyPassword1!")
	if !errors.Is(err, repositoryError) {
		t.Fatalf("Login() error = %v, want repository error", err)
	}
	if tokens != nil {
		t.Errorf("Login() tokens = %#v, want nil", tokens)
	}
}

func TestLogin_RejectsActiveAccountLock(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		lockedUntil *time.Time
	}{
		{name: "lock without expiry"},
		{name: "temporary lock", lockedUntil: timePointer(fixedNow.Add(time.Minute))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &loginUserRepository{user: &domain.User{
				ID:          uuid.New(),
				Username:    "admin",
				IsLocked:    true,
				LockedUntil: tt.lockedUntil,
			}}
			auth := newLoginTestService(t, users, fixedNow)

			tokens, err := auth.Login(context.Background(), "admin", "AnyPassword1!")
			if !errors.Is(err, ErrAccountLocked) {
				t.Fatalf("Login() error = %v, want ErrAccountLocked", err)
			}
			if tokens != nil {
				t.Errorf("Login() tokens = %#v, want nil", tokens)
			}
			if users.updateCalls != 0 {
				t.Errorf("Update() calls = %d, want 0", users.updateCalls)
			}
		})
	}
}

func TestLogin_InvalidPasswordUpdatesFailureState(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name               string
		initialAttempts    int
		wantAttempts       int
		wantLocked         bool
		wantLockedDuration time.Duration
	}{
		{
			name:            "below lock threshold",
			initialAttempts: 3,
			wantAttempts:    4,
		},
		{
			name:               "five failures",
			initialAttempts:    4,
			wantAttempts:       5,
			wantLocked:         true,
			wantLockedDuration: 5 * time.Minute,
		},
		{
			name:               "ten failures",
			initialAttempts:    9,
			wantAttempts:       10,
			wantLocked:         true,
			wantLockedDuration: 30 * time.Minute,
		},
		{
			name:               "twenty failures",
			initialAttempts:    19,
			wantAttempts:       20,
			wantLocked:         true,
			wantLockedDuration: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &loginUserRepository{}
			auth := newLoginTestService(t, users, fixedNow)
			users.user = newLoginTestUser(t, auth, "CorrectPassword1!")
			users.user.FailedAttempts = tt.initialAttempts
			ctx := context.WithValue(context.Background(), struct{}{}, tt.name)

			tokens, err := auth.Login(ctx, "admin", "WrongPassword1!")
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
			if tokens != nil {
				t.Errorf("Login() tokens = %#v, want nil", tokens)
			}
			if users.updateCalls != 1 || users.updatedUser != users.user {
				t.Fatalf("Update() calls = %d with user %p, want once with %p", users.updateCalls, users.updatedUser, users.user)
			}
			if users.receivedCtx != ctx {
				t.Error("Login() did not pass context to repository")
			}
			if users.user.FailedAttempts != tt.wantAttempts {
				t.Errorf("FailedAttempts = %d, want %d", users.user.FailedAttempts, tt.wantAttempts)
			}
			if users.user.IsLocked != tt.wantLocked {
				t.Errorf("IsLocked = %t, want %t", users.user.IsLocked, tt.wantLocked)
			}
			if tt.wantLockedDuration == 0 {
				if users.user.LockedUntil != nil {
					t.Errorf("LockedUntil = %v, want nil", users.user.LockedUntil)
				}
				return
			}
			if users.user.LockedUntil == nil {
				t.Fatal("LockedUntil = nil, want lock expiry")
			}
			wantLockedUntil := fixedNow.Add(tt.wantLockedDuration)
			if !users.user.LockedUntil.Equal(wantLockedUntil) {
				t.Errorf("LockedUntil = %s, want %s", users.user.LockedUntil, wantLockedUntil)
			}
		})
	}
}

func TestLogin_SuccessResetsStateAndReturnsTokenPair(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	users := &loginUserRepository{}
	auth := newLoginTestService(t, users, fixedNow)
	users.user = newLoginTestUser(t, auth, "CorrectPassword1!")
	users.user.FailedAttempts = 9
	users.user.IsLocked = true
	users.user.LockedUntil = timePointer(fixedNow.Add(-time.Minute))
	ctx := context.WithValue(context.Background(), struct{}{}, "successful login")

	tokens, err := auth.Login(ctx, "admin", "CorrectPassword1!")
	if err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	if tokens == nil || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("Login() tokens = %#v, want access and refresh tokens", tokens)
	}
	if users.user.FailedAttempts != 0 || users.user.IsLocked || users.user.LockedUntil != nil {
		t.Errorf("login state = %#v, want reset failure and lock fields", users.user)
	}
	if users.user.LastLogin == nil || !users.user.LastLogin.Equal(fixedNow) {
		t.Errorf("LastLogin = %v, want %s", users.user.LastLogin, fixedNow)
	}
	if users.updateCalls != 1 || users.receivedCtx != ctx {
		t.Errorf("Update() calls = %d and context forwarded = %t, want one and true", users.updateCalls, users.receivedCtx == ctx)
	}

	accessClaims, err := auth.ParseToken(tokens.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("parsing access token: %v", err)
	}
	if accessClaims.Subject != users.user.ID.String() || accessClaims.Username != users.user.Username {
		t.Errorf("access claims = %#v, want logged-in user", accessClaims)
	}
	refreshClaims, err := auth.ParseToken(tokens.RefreshToken, TokenTypeRefresh)
	if err != nil {
		t.Fatalf("parsing refresh token: %v", err)
	}
	if refreshClaims.Subject != users.user.ID.String() || refreshClaims.ID == "" {
		t.Errorf("refresh claims = %#v, want logged-in user and JTI", refreshClaims)
	}
}

func TestLogin_ReturnsUpdateError(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	updateError := errors.New("test update error")
	tests := []struct {
		name     string
		password string
	}{
		{name: "invalid password", password: "WrongPassword1!"},
		{name: "valid password", password: "CorrectPassword1!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &loginUserRepository{updateErr: updateError}
			auth := newLoginTestService(t, users, fixedNow)
			users.user = newLoginTestUser(t, auth, "CorrectPassword1!")

			tokens, err := auth.Login(context.Background(), "admin", tt.password)
			if !errors.Is(err, updateError) {
				t.Fatalf("Login() error = %v, want update error", err)
			}
			if tokens != nil {
				t.Errorf("Login() tokens = %#v, want nil", tokens)
			}
		})
	}
}

func newLoginTestService(
	t *testing.T,
	users repository.UserRepository,
	now time.Time,
) *authService {
	t.Helper()

	auth, err := NewAuthService(users, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}
	concrete := auth.(*authService)
	concrete.now = func() time.Time { return now }
	return concrete
}

func newLoginTestUser(t *testing.T, auth *authService, password string) *domain.User {
	t.Helper()

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	return &domain.User{
		ID:           uuid.New(),
		Username:     "admin",
		PasswordHash: passwordHash,
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
