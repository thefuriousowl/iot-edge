package service

import (
	"errors"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account locked")
)

func lockDurationForFailedAttempts(failedAttempts int) time.Duration {

	if failedAttempts >= 20 {
		return 24 * time.Hour
	}

	if failedAttempts >= 10 {
		return 30 * time.Minute
	}

	if failedAttempts >= 5 {
		return 5 * time.Minute
	}

	return 0

}
