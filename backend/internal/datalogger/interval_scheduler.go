package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSnapshotReaderRequired = errors.New("Data Logger snapshot reader is required")
	ErrHistoryWriterRequired  = errors.New("Data Logger history writer is required")
	ErrSchedulerClockRequired = errors.New("Data Logger scheduler clock is required")
	ErrInvalidIntervalLogger  = errors.New("invalid interval Data Logger")
	ErrIntervalLoggerDisabled = errors.New("interval Data Logger is disabled")
)

const (
	missingSnapshotError = "latest Tag value is unavailable"
	typeMismatchError    = "latest Tag value data type does not match"
	invalidQualityError  = "latest Tag value quality is invalid"
)

type SnapshotValue struct {
	ObservedAt time.Time
	DataType   string
	Value      any
	Quality    string
	Error      string
}

type SnapshotReader interface {
	Snapshot([]uuid.UUID) map[uuid.UUID]SnapshotValue
}

type HistoryWriter interface {
	WriteBatch(context.Context, RawBatch) error
}

type SchedulerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type SchedulerClock interface {
	Now() time.Time
	NewTimer(time.Duration) SchedulerTimer
}

type IntervalSchedulerOption func(*IntervalScheduler)

type IntervalScheduler struct {
	snapshots SnapshotReader
	history   HistoryWriter
	clock     SchedulerClock
	onError   func(uuid.UUID, time.Time, error)
}

func NewIntervalScheduler(snapshots SnapshotReader, history HistoryWriter, options ...IntervalSchedulerOption) (*IntervalScheduler, error) {
	if snapshots == nil {
		return nil, ErrSnapshotReaderRequired
	}
	if history == nil {
		return nil, ErrHistoryWriterRequired
	}
	scheduler := &IntervalScheduler{snapshots: snapshots, history: history, clock: realSchedulerClock{}, onError: func(uuid.UUID, time.Time, error) {}}
	for _, option := range options {
		if option != nil {
			option(scheduler)
		}
	}
	if scheduler.clock == nil {
		return nil, ErrSchedulerClockRequired
	}
	return scheduler, nil
}

func WithIntervalSchedulerClock(clock SchedulerClock) IntervalSchedulerOption {
	return func(scheduler *IntervalScheduler) { scheduler.clock = clock }
}

func WithIntervalSchedulerErrorHandler(handler func(uuid.UUID, time.Time, error)) IntervalSchedulerOption {
	return func(scheduler *IntervalScheduler) {
		if handler != nil {
			scheduler.onError = handler
		}
	}
}

func (scheduler *IntervalScheduler) Run(ctx context.Context, logger Logger) error {
	interval, err := validateIntervalLogger(logger)
	if err != nil {
		return err
	}
	scheduledAt := nextIntervalRun(logger.StartAt.UTC(), interval, scheduler.clock.Now().UTC())
	for {
		if logger.EndAt != nil && scheduledAt.After(logger.EndAt.UTC()) {
			return nil
		}
		if err := waitForSchedule(ctx, scheduler.clock, scheduledAt); err != nil {
			return err
		}
		now := scheduler.clock.Now().UTC()
		if now.After(scheduledAt) {
			latest := latestIntervalRun(logger.StartAt.UTC(), interval, now)
			if latest.After(scheduledAt) {
				scheduledAt = latest
			}
		}
		if logger.EndAt != nil && scheduledAt.After(logger.EndAt.UTC()) {
			return nil
		}
		if err := scheduler.Capture(ctx, logger, scheduledAt); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			scheduler.onError(logger.ID, scheduledAt, err)
		}
		scheduledAt = scheduledAt.Add(interval)
	}
}

func (scheduler *IntervalScheduler) Capture(ctx context.Context, logger Logger, batchAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := validateIntervalLogger(logger); err != nil {
		return err
	}
	if batchAt.IsZero() {
		return ErrInvalidIntervalLogger
	}
	batchAt = batchAt.UTC()
	if batchAt.Before(logger.StartAt.UTC()) || logger.EndAt != nil && batchAt.After(logger.EndAt.UTC()) {
		return ErrInvalidIntervalLogger
	}
	return captureLatestSnapshot(ctx, scheduler.snapshots, scheduler.history, logger, batchAt)
}

func captureLatestSnapshot(ctx context.Context, snapshots SnapshotReader, history HistoryWriter, logger Logger, batchAt time.Time) error {
	tagIDs := make([]uuid.UUID, 0, len(logger.Tags))
	for _, reference := range logger.Tags {
		tagIDs = append(tagIDs, reference.ID)
	}
	snapshot := snapshots.Snapshot(tagIDs)
	samples := make([]RawSample, 0, len(logger.Tags))
	for _, reference := range logger.Tags {
		value, exists := snapshot[reference.ID]
		samples = append(samples, rawSampleFromSnapshot(reference, value, exists, batchAt))
	}
	return history.WriteBatch(ctx, RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: samples})
}

func validateIntervalLogger(logger Logger) (time.Duration, error) {
	if logger.ID == uuid.Nil || logger.Mode != ModeInterval || logger.StartAt.IsZero() || len(logger.Tags) == 0 {
		return 0, ErrInvalidIntervalLogger
	}
	if !logger.Enabled {
		return 0, ErrIntervalLoggerDisabled
	}
	if logger.EndAt != nil && !logger.EndAt.After(logger.StartAt) {
		return 0, ErrInvalidIntervalLogger
	}
	normalized, err := normalizeConfig(ModeInterval, logger.Config)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidIntervalLogger, err)
	}
	var config IntervalConfig
	if err := json.Unmarshal(normalized, &config); err != nil || config.IntervalSeconds < 1 || config.IntervalSeconds > MaxIntervalSeconds {
		return 0, ErrInvalidIntervalLogger
	}
	for _, reference := range logger.Tags {
		if reference.ID == uuid.Nil || !validHistoryDataType(reference.DataType) {
			return 0, ErrInvalidIntervalLogger
		}
	}
	return time.Duration(config.IntervalSeconds) * time.Second, nil
}

func rawSampleFromSnapshot(reference TagReference, value SnapshotValue, exists bool, batchAt time.Time) RawSample {
	sample := RawSample{TagID: reference.ID, ObservedAt: value.ObservedAt.UTC(), DataType: reference.DataType}
	bad := func(message string) RawSample {
		sample.Quality = RawQualityBad
		sample.Value = nil
		sample.Error = message
		if sample.ObservedAt.IsZero() {
			sample.ObservedAt = batchAt
		}
		return sample
	}
	if !exists || value.ObservedAt.IsZero() {
		return bad(missingSnapshotError)
	}
	if value.DataType != reference.DataType {
		return bad(typeMismatchError)
	}
	switch value.Quality {
	case RawQualityGood:
		sample.Quality = RawQualityGood
		sample.Value = value.Value
		return sample
	case RawQualityBad:
		message := value.Error
		if message == "" {
			message = invalidQualityError
		}
		return bad(message)
	default:
		return bad(invalidQualityError)
	}
}

func nextIntervalRun(startAt time.Time, interval time.Duration, now time.Time) time.Time {
	if !now.After(startAt) {
		return startAt
	}
	elapsed := now.Sub(startAt)
	steps := elapsed / interval
	candidate := startAt.Add(steps * interval)
	if candidate.Before(now) {
		candidate = candidate.Add(interval)
	}
	return candidate
}

func latestIntervalRun(startAt time.Time, interval time.Duration, now time.Time) time.Time {
	if !now.After(startAt) {
		return startAt
	}
	return startAt.Add((now.Sub(startAt) / interval) * interval)
}

func waitForSchedule(ctx context.Context, clock SchedulerClock, scheduledAt time.Time) error {
	wait := scheduledAt.Sub(clock.Now().UTC())
	if wait <= 0 {
		return ctx.Err()
	}
	timer := clock.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C():
		return nil
	}
}

type realSchedulerClock struct{}

func (realSchedulerClock) Now() time.Time { return time.Now().UTC() }

func (realSchedulerClock) NewTimer(duration time.Duration) SchedulerTimer {
	return schedulerTimer{timer: time.NewTimer(duration)}
}

type schedulerTimer struct{ timer *time.Timer }

func (timer schedulerTimer) C() <-chan time.Time { return timer.timer.C }
func (timer schedulerTimer) Stop() bool          { return timer.timer.Stop() }
