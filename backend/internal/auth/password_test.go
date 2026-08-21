package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestValidatePassword(t *testing.T) {
	service := newTestAuthService(t)
	tests := []struct {
		name      string
		username  string
		candidate string
		wantErr   error
	}{
		{
			name:      "three character classes",
			username:  "admin",
			candidate: "StrongPass123",
		},
		{
			name:      "all four character classes",
			username:  "admin",
			candidate: strongCredentialFixture(),
		},
		{
			name:      "too short",
			username:  "admin",
			candidate: "Aa1!aaa",
			wantErr:   ErrPasswordTooShort,
		},
		{
			name:      "too long",
			username:  "admin",
			candidate: strings.Repeat("a", 126) + "A1!",
			wantErr:   ErrPasswordTooLong,
		},
		{
			name:      "insufficient character classes",
			username:  "admin",
			candidate: "alllowercase",
			wantErr:   ErrPasswordComplexity,
		},
		{
			name:      "uncased letters are not special characters",
			username:  "admin",
			candidate: "lowercase1ภาษาไทย",
			wantErr:   ErrPasswordComplexity,
		},
		{
			name:      "contains username case insensitively",
			username:  "admin",
			candidate: "PrefixADMIN123!",
			wantErr:   ErrPasswordHasUsername,
		},
		{
			name:      "invalid UTF-8",
			username:  "admin",
			candidate: string([]byte{'A', 'a', '1', '!', 0xff, 'a', 'a', 'a'}),
			wantErr:   ErrPasswordRequirements,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.ValidatePassword(tt.username, tt.candidate)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("ValidatePassword() unexpected error: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidatePassword() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func strongCredentialFixture() string {
	return strings.Join([]string{"Secure", "P@ss", "123"}, "")
}

func TestHashAndVerifyPassword_UsesSHA256PrehashAndBcryptCost12(t *testing.T) {
	service := newTestAuthService(t)
	password := strings.Repeat("x", 80) + "A1!"
	differentAfterBcryptLimit := strings.Repeat("x", 80) + "A2!"

	if err := service.ValidatePassword("admin", password); err != nil {
		t.Fatalf("test password failed validation: %v", err)
	}
	hash, err := service.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if hash == password {
		t.Fatal("HashPassword() returned plaintext")
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("reading bcrypt cost: %v", err)
	}
	if cost != passwordBcryptCost {
		t.Errorf("bcrypt cost = %d, want %d", cost, passwordBcryptCost)
	}
	if !service.VerifyPassword(password, hash) {
		t.Fatal("VerifyPassword() rejected the correct password")
	}
	if service.VerifyPassword(differentAfterBcryptLimit, hash) {
		t.Fatal("VerifyPassword() accepted a password differing after bcrypt's 72-byte limit")
	}
	if service.VerifyPassword(password, "not-a-bcrypt-hash") {
		t.Fatal("VerifyPassword() accepted an invalid bcrypt hash")
	}
}
