package report

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

var (
	ErrNotFound           = errors.New("report not found")
	ErrNameExists         = errors.New("report name already exists")
	ErrColumnNameExists   = errors.New("report column name already exists")
	ErrTagNotSelected     = errors.New("report Tag is not selected by Data Logger")
	ErrInvalidReport      = errors.New("invalid report")
	ErrInvalidInput       = errors.New("invalid report input")
	ErrRepositoryRequired = errors.New("report repository is required")
	ErrLoggerRequired     = errors.New("data logger repository is required")
	ErrQueryRequired      = errors.New("report query repository is required")
)

type ListInput struct {
	LoggerID *uuid.UUID
	Mode     *datalogger.QueryMode
	Search   string
	Page     int
	PerPage  int
}

type ListResult struct {
	Data       []Report
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type Repository interface {
	Create(context.Context, *Report) error
	Find(context.Context, uuid.UUID) (*Report, error)
	List(context.Context, ListInput) (*ListResult, error)
	Update(context.Context, *Report) error
	Delete(context.Context, uuid.UUID) error
}

type LoggerRepository interface {
	Find(context.Context, uuid.UUID) (*datalogger.Logger, error)
}
