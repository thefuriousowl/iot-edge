package dataloggerpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

func TestSelectRetentionCombinesAgeAndSizeAtWholeBatchBoundaries(t *testing.T) {
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	maxAgeSeconds := int64((3 * time.Hour) / time.Second)
	maxSizeBytes := int64(200)
	batches := []batchStorageRow{
		{BatchAt: evaluatedAt.Add(-time.Hour), RowCount: 2, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-2 * time.Hour), RowCount: 2, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-3 * time.Hour), RowCount: 2, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-4 * time.Hour), RowCount: 2, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-5 * time.Hour), RowCount: 2, EstimatedSizeBytes: 100},
	}

	selection, err := selectRetention(loggerStoragePolicy{MaxSizeBytes: &maxSizeBytes, MaxAgeSeconds: &maxAgeSeconds}, batches, evaluatedAt)
	if err != nil {
		t.Fatalf("selectRetention() error = %v", err)
	}
	if selection.Plan.CutoffAt == nil || !selection.Plan.CutoffAt.Equal(evaluatedAt.Add(-3*time.Hour)) {
		t.Fatalf("cutoff = %v", selection.Plan.CutoffAt)
	}
	if selection.Plan.Current.BatchCount != 5 || selection.Plan.Remove.BatchCount != 3 || selection.Plan.EstimatedRetained.BatchCount != 2 {
		t.Fatalf("plan = %#v", selection.Plan)
	}
	if len(selection.Candidates) != 3 || !selection.Candidates[0].BatchAt.Equal(evaluatedAt.Add(-5*time.Hour)) || !selection.Candidates[2].BatchAt.Equal(evaluatedAt.Add(-3*time.Hour)) {
		t.Errorf("oldest-first candidates = %#v", selection.Candidates)
	}
	if selection.Plan.EstimatedRetained.OldestBatchAt == nil || !selection.Plan.EstimatedRetained.OldestBatchAt.Equal(evaluatedAt.Add(-2*time.Hour)) {
		t.Errorf("estimated retained = %#v", selection.Plan.EstimatedRetained)
	}
}

func TestSelectRetentionKeepsExactAgeCutoffAndUnlimitedHistory(t *testing.T) {
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	maxAgeSeconds := int64(3600)
	batches := []batchStorageRow{
		{BatchAt: evaluatedAt, RowCount: 1, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-time.Hour), RowCount: 1, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-time.Hour - time.Nanosecond), RowCount: 1, EstimatedSizeBytes: 100},
	}

	ageSelection, err := selectRetention(loggerStoragePolicy{MaxAgeSeconds: &maxAgeSeconds}, batches, evaluatedAt)
	if err != nil {
		t.Fatalf("selectRetention(age) error = %v", err)
	}
	if ageSelection.Plan.Remove.BatchCount != 1 || !ageSelection.Candidates[0].BatchAt.Equal(evaluatedAt.Add(-time.Hour-time.Nanosecond)) {
		t.Errorf("age selection = %#v", ageSelection)
	}
	unlimited, err := selectRetention(loggerStoragePolicy{}, batches, evaluatedAt)
	if err != nil || unlimited.Plan.Remove.BatchCount != 0 || unlimited.Plan.EstimatedRetained.BatchCount != 3 || unlimited.Plan.CutoffAt != nil {
		t.Errorf("unlimited selection = %#v, %v", unlimited, err)
	}
}

func TestSelectRetentionRejectsNewestSurvivingBatchAboveSizeLimit(t *testing.T) {
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	maxSizeBytes := int64(99)
	_, err := selectRetention(loggerStoragePolicy{MaxSizeBytes: &maxSizeBytes}, []batchStorageRow{{BatchAt: evaluatedAt, RowCount: 1, EstimatedSizeBytes: 100}}, evaluatedAt)
	if !errors.Is(err, datalogger.ErrStorageLimitTooSmall) {
		t.Errorf("selectRetention() error = %v", err)
	}
}

func TestSelectRetentionKeepsAContiguousNewestSuffixWhenBatchSizesDiffer(t *testing.T) {
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	maxSizeBytes := int64(150)
	batches := []batchStorageRow{
		{BatchAt: evaluatedAt, RowCount: 1, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-time.Hour), RowCount: 1, EstimatedSizeBytes: 100},
		{BatchAt: evaluatedAt.Add(-2 * time.Hour), RowCount: 1, EstimatedSizeBytes: 40},
	}
	selection, err := selectRetention(loggerStoragePolicy{MaxSizeBytes: &maxSizeBytes}, batches, evaluatedAt)
	if err != nil {
		t.Fatalf("selectRetention() error = %v", err)
	}
	if selection.Plan.EstimatedRetained.BatchCount != 1 || selection.Plan.EstimatedRetained.EstimatedSizeBytes != 100 || len(selection.Candidates) != 2 {
		t.Fatalf("selection = %#v", selection)
	}
	if !selection.Candidates[0].BatchAt.Equal(evaluatedAt.Add(-2*time.Hour)) || !selection.Candidates[1].BatchAt.Equal(evaluatedAt.Add(-time.Hour)) {
		t.Errorf("FIFO candidates = %#v", selection.Candidates)
	}
}
