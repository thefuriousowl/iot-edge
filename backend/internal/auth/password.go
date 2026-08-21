package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	passwordMinLength  = 8
	passwordMaxLength  = 128
	passwordMinClasses = 3
	passwordBcryptCost = 12
)

var (
	ErrPasswordRequirements = errors.New("password requirements not met")
	ErrPasswordTooShort     = fmt.Errorf("%w: password must contain at least %d characters", ErrPasswordRequirements, passwordMinLength)
	ErrPasswordTooLong      = fmt.Errorf("%w: password must contain at most %d characters", ErrPasswordRequirements, passwordMaxLength)
	ErrPasswordComplexity   = fmt.Errorf("%w: password must contain at least %d character types", ErrPasswordRequirements, passwordMinClasses)
	ErrPasswordHasUsername  = fmt.Errorf("%w: password must not contain the username", ErrPasswordRequirements)
)

func (s *authService) ValidatePassword(username, password string) error {
	if !utf8.ValidString(password) {
		return ErrPasswordRequirements
	}

	length := utf8.RuneCountInString(password)
	if length < passwordMinLength {
		return ErrPasswordTooShort
	}
	if length > passwordMaxLength {
		return ErrPasswordTooLong
	}

	if username != "" && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		return ErrPasswordHasUsername
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, character := range password {
		switch {
		case unicode.IsUpper(character):
			hasUpper = true
		case unicode.IsLower(character):
			hasLower = true
		case unicode.IsDigit(character):
			hasDigit = true
		case unicode.IsPunct(character) || unicode.IsSymbol(character):
			hasSpecial = true
		}
	}

	classes := 0
	for _, present := range []bool{hasUpper, hasLower, hasDigit, hasSpecial} {
		if present {
			classes++
		}
	}
	if classes < passwordMinClasses {
		return ErrPasswordComplexity
	}

	return nil
}

func (s *authService) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(passwordDigest(password), passwordBcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (s *authService) VerifyPassword(password, passwordHash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(passwordHash), passwordDigest(password)) == nil
}

// passwordDigest keeps bcrypt's input at a fixed 32 bytes so passwords up to
// the documented 128-character limit are not truncated by bcrypt's 72-byte
// input limit. Bcrypt still provides the per-password salt and work factor.
func passwordDigest(password string) []byte {
	digest := sha256.Sum256([]byte(password))
	return digest[:]
}
