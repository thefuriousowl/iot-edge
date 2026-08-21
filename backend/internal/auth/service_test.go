package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubUserRepository struct {
	UserRepository
}

type countUserRepository struct {
	UserRepository
	count       int64
	err         error
	receivedCtx context.Context
}

func (r *countUserRepository) CountUsers(ctx context.Context) (int64, error) {
	r.receivedCtx = ctx
	return r.count, r.err
}

type setupUserRepository struct {
	UserRepository
	receivedCtx  context.Context
	receivedUser *User
	err          error
	calls        int
}

func (r *setupUserRepository) CreateInitialUser(ctx context.Context, user *User) error {
	r.calls++
	r.receivedCtx = ctx
	r.receivedUser = user
	return r.err
}

func TestIsSetupRequired(t *testing.T) {
	repositoryError := errors.New("test repository error")
	tests := []struct {
		name          string
		count         int64
		repositoryErr error
		wantRequired  bool
		wantErr       error
	}{
		{
			name:         "required when no users exist",
			wantRequired: true,
		},
		{
			name:  "not required when a user exists",
			count: 1,
		},
		{
			name:          "repository error",
			repositoryErr: repositoryError,
			wantErr:       repositoryError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &countUserRepository{count: tt.count, err: tt.repositoryErr}
			auth, err := NewAuthService(users, validAuthConfig())
			if err != nil {
				t.Fatalf("NewAuthService() error: %v", err)
			}
			ctx := context.WithValue(context.Background(), struct{}{}, tt.name)

			required, err := auth.IsSetupRequired(ctx)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("IsSetupRequired() error = %v, want %v", err, tt.wantErr)
			}
			if required != tt.wantRequired {
				t.Errorf("IsSetupRequired() = %t, want %t", required, tt.wantRequired)
			}
			if users.receivedCtx != ctx {
				t.Error("IsSetupRequired() did not pass context to CountUsers()")
			}
		})
	}
}

func TestSetup_CreatesInitialUserWithHashedPassword(t *testing.T) {
	users := &setupUserRepository{}
	auth, err := NewAuthService(users, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}
	ctx := context.WithValue(context.Background(), struct{}{}, "setup")
	const (
		username = "owner"
		password = "Str0ng!Password"
	)

	user, err := auth.Setup(ctx, username, password)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	if user == nil {
		t.Fatal("Setup() user = nil, want created user")
	}
	if users.calls != 1 {
		t.Errorf("CreateInitialUser() calls = %d, want 1", users.calls)
	}
	if users.receivedCtx != ctx {
		t.Error("Setup() did not pass context to CreateInitialUser()")
	}
	if users.receivedUser != user {
		t.Error("Setup() did not return the user passed to CreateInitialUser()")
	}
	if user.Username != username {
		t.Errorf("Setup() username = %q, want %q", user.Username, username)
	}
	if user.PasswordHash == "" || user.PasswordHash == password {
		t.Error("Setup() did not store a password hash")
	}
	if !auth.VerifyPassword(password, user.PasswordHash) {
		t.Error("Setup() password hash does not verify the original password")
	}
}

func TestSetup_RejectsInvalidPasswordBeforeRepositoryCall(t *testing.T) {
	users := &setupUserRepository{}
	auth, err := NewAuthService(users, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}

	user, err := auth.Setup(context.Background(), "owner", "short")
	if !errors.Is(err, ErrPasswordRequirements) {
		t.Fatalf("Setup() error = %v, want ErrPasswordRequirements", err)
	}
	if user != nil {
		t.Errorf("Setup() user = %#v, want nil", user)
	}
	if users.calls != 0 {
		t.Errorf("CreateInitialUser() calls = %d, want 0", users.calls)
	}
}

func TestSetup_MapsInitialUserExistsError(t *testing.T) {
	users := &setupUserRepository{err: ErrInitialUserExists}
	auth, err := NewAuthService(users, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}

	user, err := auth.Setup(context.Background(), "owner", "Str0ng!Password")
	if !errors.Is(err, ErrSetupAlreadyCompleted) {
		t.Fatalf("Setup() error = %v, want ErrSetupAlreadyCompleted", err)
	}
	if user != nil {
		t.Errorf("Setup() user = %#v, want nil", user)
	}
}

func TestSetup_ReturnsRepositoryError(t *testing.T) {
	repositoryError := errors.New("test repository error")
	users := &setupUserRepository{err: repositoryError}
	auth, err := NewAuthService(users, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}

	user, err := auth.Setup(context.Background(), "owner", "Str0ng!Password")
	if !errors.Is(err, repositoryError) {
		t.Fatalf("Setup() error = %v, want repository error", err)
	}
	if user != nil {
		t.Errorf("Setup() user = %#v, want nil", user)
	}
}

func TestNewAuthService_ValidatesDependenciesAndConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		users   UserRepository
		cfg     *ServiceConfig
		wantErr error
	}{
		{
			name:    "repository required",
			cfg:     validAuthConfig(),
			wantErr: ErrUserRepositoryRequired,
		},
		{
			name:    "config required",
			users:   &stubUserRepository{},
			wantErr: ErrInvalidAuthConfig,
		},
		{
			name:  "JWT secret must be at least 256 bits",
			users: &stubUserRepository{},
			cfg: &ServiceConfig{
				JWTSecret:        "too-short",
				JWTAccessExpiry:  15 * time.Minute,
				JWTRefreshExpiry: 7 * 24 * time.Hour,
			},
			wantErr: ErrInvalidAuthConfig,
		},
		{
			name:  "access expiry must be positive",
			users: &stubUserRepository{},
			cfg: &ServiceConfig{
				JWTSecret:        strings.Repeat("s", 32),
				JWTRefreshExpiry: 7 * 24 * time.Hour,
			},
			wantErr: ErrInvalidAuthConfig,
		},
		{
			name:  "refresh expiry must be positive",
			users: &stubUserRepository{},
			cfg: &ServiceConfig{
				JWTSecret:       strings.Repeat("s", 32),
				JWTAccessExpiry: 15 * time.Minute,
			},
			wantErr: ErrInvalidAuthConfig,
		},
		{
			name:  "valid configuration",
			users: &stubUserRepository{},
			cfg:   validAuthConfig(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewAuthService(tt.users, tt.cfg)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewAuthService() error = %v, want %v", err, tt.wantErr)
				}
				if service != nil {
					t.Fatal("NewAuthService() returned a service with invalid configuration")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewAuthService() unexpected error: %v", err)
			}
			if service == nil {
				t.Fatal("NewAuthService() returned nil")
			}
		})
	}
}

func validAuthConfig() *ServiceConfig {
	return &ServiceConfig{
		JWTSecret:        strings.Repeat("s", 32),
		JWTAccessExpiry:  15 * time.Minute,
		JWTRefreshExpiry: 7 * 24 * time.Hour,
	}
}

func newTestAuthService(t *testing.T) *authService {
	t.Helper()

	service, err := NewAuthService(&stubUserRepository{}, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}
	concrete, ok := service.(*authService)
	if !ok {
		t.Fatalf("NewAuthService() type = %T, want *authService", service)
	}
	return concrete
}
