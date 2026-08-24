package datalogger

import (
	"time"
)

const (
	DefaultRetentionBatchLimit = 1000
	MaxRetentionBatchLimit     = 10000
)

type RetentionPolicy struct {
	MaxSizeBytes  *int64 `json:"max_size_bytes"`
	MaxAgeSeconds *int64 `json:"max_age_seconds"`
}

type RetentionMetrics struct {
	RowCount           int64      `json:"row_count"`
	BatchCount         int64      `json:"batch_count"`
	EstimatedSizeBytes int64      `json:"estimated_size_bytes"`
	OldestBatchAt      *time.Time `json:"oldest_batch_at"`
	NewestBatchAt      *time.Time `json:"newest_batch_at"`
}

type RetentionPlan struct {
	EvaluatedAt       time.Time        `json:"evaluated_at"`
	CutoffAt          *time.Time       `json:"cutoff_at"`
	Policy            RetentionPolicy  `json:"policy"`
	Current           RetentionMetrics `json:"current"`
	Remove            RetentionMetrics `json:"remove"`
	EstimatedRetained RetentionMetrics `json:"estimated_retained"`
}

type RetentionCleanupInput struct {
	EvaluatedAt time.Time
	BatchLimit  int
}

type RetentionCleanupResult struct {
	StartedAt        time.Time        `json:"started_at"`
	CompletedAt      time.Time        `json:"completed_at"`
	EvaluatedAt      time.Time        `json:"evaluated_at"`
	CutoffAt         *time.Time       `json:"cutoff_at"`
	Policy           RetentionPolicy  `json:"policy"`
	Deleted          RetentionMetrics `json:"deleted"`
	Retained         RetentionMetrics `json:"retained"`
	RemainingRemoval RetentionMetrics `json:"remaining_removal"`
	Complete         bool             `json:"complete"`
}

type RetentionRunStatus struct {
	StartedAt   time.Time               `json:"started_at"`
	CompletedAt time.Time               `json:"completed_at"`
	Result      *RetentionCleanupResult `json:"result"`
	Error       *string                 `json:"error"`
}

type RetentionStatus struct {
	Plan    RetentionPlan       `json:"plan"`
	LastRun *RetentionRunStatus `json:"last_run"`
}
