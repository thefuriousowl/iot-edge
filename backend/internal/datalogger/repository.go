package datalogger

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrLoggerNotFound    = errors.New("data logger not found")
	ErrLoggerNameExists  = errors.New("data logger name already exists")
	ErrLoggerTagNotFound = errors.New("selected Tag not found")
	ErrInvalidLogger     = errors.New("invalid data logger")
	ErrInvalidLoggerTag  = errors.New("invalid data logger Tag selection")
	ErrInvalidInput      = errors.New("invalid data logger input")
)

type ListInput struct {
	Mode    *Mode
	Enabled *bool
	Search  string
	Page    int
	PerPage int
}

type ListResult struct {
	Data       []Logger
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type Repository interface {
	Create(context.Context, *Logger, []uuid.UUID) error
	Find(context.Context, uuid.UUID) (*Logger, error)
	List(context.Context, ListInput) (*ListResult, error)
	Update(context.Context, *Logger, []uuid.UUID) error
	Delete(context.Context, uuid.UUID) error
}

type RetentionRuntimeRepository interface {
	ListRetentionLoggerIDs(context.Context) ([]uuid.UUID, error)
}
