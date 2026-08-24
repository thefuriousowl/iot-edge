package datalogger

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	RawQualityGood = "good"
	RawQualityBad  = "bad"
)

var (
	ErrInvalidRawBatch      = errors.New("invalid Data Logger batch")
	ErrRawBatchNotFound     = errors.New("Data Logger batch not found")
	ErrRawTagNotSelected    = errors.New("Tag is not selected by Data Logger")
	ErrStorageLimitTooSmall = errors.New("Data Logger storage limit cannot hold one complete batch")
	ErrRetentionCleanup     = errors.New("Data Logger retention cleanup failed")
)

type RawSample struct {
	TagID      uuid.UUID `json:"tag_id"`
	ObservedAt time.Time `json:"observed_at"`
	DataType   string    `json:"data_type"`
	Value      any       `json:"value"`
	Quality    string    `json:"quality"`
	Error      string    `json:"error,omitempty"`
}

type RawBatch struct {
	LoggerID uuid.UUID
	BatchAt  time.Time
	Samples  []RawSample
}

type RawBatchListInput struct {
	LoggerID         uuid.UUID
	TagIDs           []uuid.UUID
	From             time.Time
	To               time.Time
	IncludeNeighbors bool
}

type RawValue struct {
	LoggerID    uuid.UUID `json:"logger_id"`
	TagID       uuid.UUID `json:"tag_id"`
	BatchAt     time.Time `json:"batch_at"`
	ObservedAt  time.Time `json:"observed_at"`
	DataType    string    `json:"data_type"`
	Value       any       `json:"value"`
	Quality     string    `json:"quality"`
	Error       string    `json:"error,omitempty"`
	PersistedAt time.Time `json:"persisted_at"`
}

type RawValueListInput struct {
	LoggerID uuid.UUID
	TagID    *uuid.UUID
	From     *time.Time
	To       *time.Time
	Page     int
	PerPage  int
}

type RawValueListResult struct {
	Data        []RawValue
	Page        int
	PerPage     int
	Total       int64
	TotalPages  int
	LastBatchAt *time.Time
}

type HistoryRepository interface {
	WriteBatch(context.Context, RawBatch) error
	LatestBatch(context.Context, uuid.UUID) (*RawBatch, error)
	ListBatches(context.Context, RawBatchListInput) ([]RawBatch, error)
	ListValues(context.Context, RawValueListInput) (*RawValueListResult, error)
	Query(context.Context, QueryInput) (*QueryResult, error)
	ManagementOverview(context.Context, time.Time) (*DataManagementOverview, error)
	Storage(context.Context, uuid.UUID, int, *int64) (*StorageStats, error)
	Retention(context.Context, uuid.UUID, time.Time) (*RetentionStatus, error)
	CleanupRetention(context.Context, uuid.UUID, RetentionCleanupInput) (*RetentionCleanupResult, error)
	ValidateStorageLimit(context.Context, uuid.UUID, *int64) error
	EnforceRetention(context.Context, uuid.UUID, RetentionPolicy) error
}

func validHistoryDataType(value string) bool {
	switch value {
	case "bool", "int16", "uint16", "int32", "uint32", "float32", "float64":
		return true
	default:
		return false
	}
}
