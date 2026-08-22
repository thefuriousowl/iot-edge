package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewIntervalSchedulerValidatesDependencies(t *testing.T) {
	t.Parallel()
	snapshots := &schedulerSnapshotReader{}
	history := &schedulerHistoryWriter{}
	tests := []struct {
		name      string
		snapshots SnapshotReader
		history   HistoryWriter
		options   []IntervalSchedulerOption
		want      error
	}{
		{name: "snapshot reader", history: history, want: ErrSnapshotReaderRequired},
		{name: "history writer", snapshots: snapshots, want: ErrHistoryWriterRequired},
		{name: "clock", snapshots: snapshots, history: history, options: []IntervalSchedulerOption{WithIntervalSchedulerClock(nil)}, want: ErrSchedulerClockRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewIntervalScheduler(test.snapshots, test.history, test.options...); !errors.Is(err, test.want) {
				t.Fatalf("NewIntervalScheduler() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestIntervalSchedulerCapturesOneAtomicLatestSnapshot(t *testing.T) {
	t.Parallel()
	batchAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	observedAt := batchAt.Add(-time.Second)
	tags := []TagReference{
		{ID: uuid.New(), Name: "Good", DataType: "float64", Enabled: true},
		{ID: uuid.New(), Name: "Bad", DataType: "uint16", Enabled: true},
		{ID: uuid.New(), Name: "Missing", DataType: "bool", Enabled: true},
		{ID: uuid.New(), Name: "Mismatch", DataType: "float32", Enabled: true},
		{ID: uuid.New(), Name: "Invalid quality", DataType: "int32", Enabled: true},
	}
	snapshots := &schedulerSnapshotReader{values: map[uuid.UUID]SnapshotValue{
		tags[0].ID: {ObservedAt: observedAt, DataType: "float64", Value: float64(12.5), Quality: RawQualityGood},
		tags[1].ID: {ObservedAt: observedAt.Add(time.Millisecond), DataType: "uint16", Quality: RawQualityBad, Error: "illegal data address"},
		tags[3].ID: {ObservedAt: observedAt, DataType: "uint16", Value: uint16(1), Quality: RawQualityGood},
		tags[4].ID: {ObservedAt: observedAt, DataType: "int32", Value: int32(1), Quality: "uncertain"},
	}}
	history := &schedulerHistoryWriter{}
	scheduler := newScheduler(t, snapshots, history)
	logger := intervalLogger(tags, batchAt.Add(-time.Hour), nil)

	if err := scheduler.Capture(context.Background(), logger, batchAt); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !reflect.DeepEqual(snapshots.requested, []uuid.UUID{tags[0].ID, tags[1].ID, tags[2].ID, tags[3].ID, tags[4].ID}) {
		t.Errorf("Snapshot() Tag IDs = %v", snapshots.requested)
	}
	if len(history.batches) != 1 {
		t.Fatalf("history batches = %d, want 1", len(history.batches))
	}
	batch := history.batches[0]
	if batch.LoggerID != logger.ID || !batch.BatchAt.Equal(batchAt) || len(batch.Samples) != len(tags) {
		t.Fatalf("batch = %#v", batch)
	}
	if batch.Samples[0].Quality != RawQualityGood || batch.Samples[0].Value != float64(12.5) || !batch.Samples[0].ObservedAt.Equal(observedAt) {
		t.Errorf("good sample = %#v", batch.Samples[0])
	}
	if batch.Samples[1].Quality != RawQualityBad || batch.Samples[1].Error != "illegal data address" || !batch.Samples[1].ObservedAt.Equal(observedAt.Add(time.Millisecond)) {
		t.Errorf("bad sample = %#v", batch.Samples[1])
	}
	if batch.Samples[2].Quality != RawQualityBad || batch.Samples[2].Error != missingSnapshotError || !batch.Samples[2].ObservedAt.Equal(batchAt) {
		t.Errorf("missing sample = %#v", batch.Samples[2])
	}
	if batch.Samples[3].Quality != RawQualityBad || batch.Samples[3].Error != typeMismatchError || batch.Samples[3].DataType != "float32" {
		t.Errorf("mismatched sample = %#v", batch.Samples[3])
	}
	if batch.Samples[4].Quality != RawQualityBad || batch.Samples[4].Error != invalidQualityError {
		t.Errorf("invalid-quality sample = %#v", batch.Samples[4])
	}
}

func TestIntervalSchedulerValidatesLoggerAndCaptureBoundaries(t *testing.T) {
	t.Parallel()
	startAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	endAt := startAt.Add(time.Hour)
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	valid := intervalLogger([]TagReference{tag}, startAt, &endAt)
	tests := []struct {
		name   string
		logger Logger
		at     time.Time
		want   error
	}{
		{name: "nil logger", logger: withLoggerID(valid, uuid.Nil), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "disabled", logger: withLoggerEnabled(valid, false), at: startAt, want: ErrIntervalLoggerDisabled},
		{name: "schedule mode", logger: withLoggerMode(valid, ModeSchedule), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "zero start", logger: withLoggerStart(valid, time.Time{}), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "no Tags", logger: withLoggerTags(valid, nil), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "nil Tag", logger: withLoggerTags(valid, []TagReference{{DataType: "float64"}}), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "bad Tag type", logger: withLoggerTags(valid, []TagReference{{ID: tag.ID, DataType: "string"}}), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "bad config", logger: withLoggerConfig(valid, json.RawMessage(`{"interval_seconds":0}`)), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "overflow config", logger: withLoggerConfig(valid, json.RawMessage(`{"interval_seconds":9223372037}`)), at: startAt, want: ErrInvalidIntervalLogger},
		{name: "before start", logger: valid, at: startAt.Add(-time.Nanosecond), want: ErrInvalidIntervalLogger},
		{name: "after end", logger: valid, at: endAt.Add(time.Nanosecond), want: ErrInvalidIntervalLogger},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler := newScheduler(t, &schedulerSnapshotReader{}, &schedulerHistoryWriter{})
			if err := scheduler.Capture(context.Background(), test.logger, test.at); !errors.Is(err, test.want) {
				t.Fatalf("Capture() error = %v, want %v", err, test.want)
			}
		})
	}
	historyError := errors.New("disk unavailable")
	scheduler := newScheduler(t, &schedulerSnapshotReader{}, &schedulerHistoryWriter{err: historyError})
	if err := scheduler.Capture(context.Background(), valid, startAt); !errors.Is(err, historyError) {
		t.Errorf("Capture(history error) = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	history := &schedulerHistoryWriter{}
	scheduler = newScheduler(t, &schedulerSnapshotReader{}, history)
	if err := scheduler.Capture(canceled, valid, startAt); !errors.Is(err, context.Canceled) || len(history.batches) != 0 {
		t.Errorf("Capture(canceled) = %v, batches = %d", err, len(history.batches))
	}
}

func TestIntervalRunCalculationsStayAnchoredToStart(t *testing.T) {
	t.Parallel()
	startAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	interval := 10 * time.Second
	tests := []struct {
		name   string
		now    time.Time
		next   time.Time
		latest time.Time
	}{
		{name: "before start", now: startAt.Add(-time.Second), next: startAt, latest: startAt},
		{name: "at start", now: startAt, next: startAt, latest: startAt},
		{name: "between runs", now: startAt.Add(25 * time.Second), next: startAt.Add(30 * time.Second), latest: startAt.Add(20 * time.Second)},
		{name: "exact run", now: startAt.Add(30 * time.Second), next: startAt.Add(30 * time.Second), latest: startAt.Add(30 * time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nextIntervalRun(startAt, interval, test.now); !got.Equal(test.next) {
				t.Errorf("nextIntervalRun() = %s, want %s", got, test.next)
			}
			if got := latestIntervalRun(startAt, interval, test.now); !got.Equal(test.latest) {
				t.Errorf("latestIntervalRun() = %s, want %s", got, test.latest)
			}
		})
	}
}

func TestIntervalSchedulerRunsFixedRateSkipsDelayedTicksAndIncludesEnd(t *testing.T) {
	startAt := time.Date(2026, time.August, 23, 8, 1, 0, 0, time.UTC)
	endAt := startAt.Add(2 * time.Minute)
	clock := newManualSchedulerClock(startAt.Add(-time.Minute))
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	snapshots := &schedulerSnapshotReader{values: map[uuid.UUID]SnapshotValue{tag.ID: {ObservedAt: startAt.Add(-time.Second), DataType: "float64", Value: float64(1), Quality: RawQualityGood}}}
	historyFailure := errors.New("temporary history failure")
	history := &schedulerHistoryWriter{errorsAt: map[time.Time]error{startAt: historyFailure}, written: make(chan RawBatch, 4)}
	reported := make(chan error, 1)
	scheduler, err := NewIntervalScheduler(snapshots, history, WithIntervalSchedulerClock(clock), WithIntervalSchedulerErrorHandler(func(_ uuid.UUID, _ time.Time, err error) { reported <- err }))
	if err != nil {
		t.Fatalf("NewIntervalScheduler() error = %v", err)
	}
	logger := intervalLogger([]TagReference{tag}, startAt, &endAt)
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(context.Background(), logger) }()

	awaitSchedulerTimer(t, clock)
	clock.Advance(startAt)
	first := awaitWrittenBatch(t, history.written)
	if !first.BatchAt.Equal(startAt) {
		t.Errorf("first batch = %s, want %s", first.BatchAt, startAt)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, historyFailure) {
			t.Errorf("reported error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("history failure was not reported")
	}

	awaitSchedulerTimer(t, clock)
	clock.Advance(endAt)
	last := awaitWrittenBatch(t, history.written)
	if !last.BatchAt.Equal(endAt) {
		t.Errorf("delayed batch = %s, want aligned end %s", last.BatchAt, endAt)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after inclusive end boundary")
	}
	if len(history.batches) != 2 {
		t.Errorf("captured batches = %d, want 2 without delayed backfill", len(history.batches))
	}
}

func TestIntervalSchedulerStopsOnContextCancellation(t *testing.T) {
	t.Parallel()
	startAt := time.Date(2026, time.August, 23, 8, 1, 0, 0, time.UTC)
	clock := newManualSchedulerClock(startAt.Add(-time.Minute))
	tag := TagReference{ID: uuid.New(), DataType: "bool", Enabled: true}
	scheduler, err := NewIntervalScheduler(&schedulerSnapshotReader{}, &schedulerHistoryWriter{}, WithIntervalSchedulerClock(clock))
	if err != nil {
		t.Fatalf("NewIntervalScheduler() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx, intervalLogger([]TagReference{tag}, startAt, nil)) }()
	awaitSchedulerTimer(t, clock)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
}

type schedulerSnapshotReader struct {
	values    map[uuid.UUID]SnapshotValue
	requested []uuid.UUID
}

func (reader *schedulerSnapshotReader) Snapshot(tagIDs []uuid.UUID) map[uuid.UUID]SnapshotValue {
	reader.requested = append([]uuid.UUID(nil), tagIDs...)
	result := make(map[uuid.UUID]SnapshotValue, len(tagIDs))
	for _, tagID := range tagIDs {
		if value, exists := reader.values[tagID]; exists {
			result[tagID] = value
		}
	}
	return result
}

type schedulerHistoryWriter struct {
	mu       sync.Mutex
	batches  []RawBatch
	err      error
	errorsAt map[time.Time]error
	written  chan RawBatch
}

func (writer *schedulerHistoryWriter) WriteBatch(_ context.Context, batch RawBatch) error {
	writer.mu.Lock()
	writer.batches = append(writer.batches, batch)
	err := writer.err
	if specific := writer.errorsAt[batch.BatchAt]; specific != nil {
		err = specific
	}
	writer.mu.Unlock()
	if writer.written != nil {
		writer.written <- batch
	}
	return err
}

type manualSchedulerClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualSchedulerTimer
}

type manualSchedulerTimer struct {
	clock    *manualSchedulerClock
	deadline time.Time
	stream   chan time.Time
	active   bool
}

func newManualSchedulerClock(now time.Time) *manualSchedulerClock {
	return &manualSchedulerClock{now: now}
}

func (clock *manualSchedulerClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *manualSchedulerClock) NewTimer(duration time.Duration) SchedulerTimer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	timer := &manualSchedulerTimer{clock: clock, deadline: clock.now.Add(duration), stream: make(chan time.Time, 1), active: true}
	clock.timers = append(clock.timers, timer)
	return timer
}

func (clock *manualSchedulerClock) Advance(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	for _, timer := range clock.timers {
		if timer.active && !timer.deadline.After(now) {
			timer.active = false
			timer.stream <- now
		}
	}
	clock.mu.Unlock()
}

func (timer *manualSchedulerTimer) C() <-chan time.Time { return timer.stream }

func (timer *manualSchedulerTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	wasActive := timer.active
	timer.active = false
	return wasActive
}

func (clock *manualSchedulerClock) activeTimers() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	count := 0
	for _, timer := range clock.timers {
		if timer.active {
			count++
		}
	}
	return count
}

func newScheduler(t *testing.T, snapshots SnapshotReader, history HistoryWriter) *IntervalScheduler {
	t.Helper()
	scheduler, err := NewIntervalScheduler(snapshots, history)
	if err != nil {
		t.Fatalf("NewIntervalScheduler() error = %v", err)
	}
	return scheduler
}

func intervalLogger(tags []TagReference, startAt time.Time, endAt *time.Time) Logger {
	return Logger{ID: uuid.New(), Name: "Interval", Enabled: true, Timezone: "UTC", Mode: ModeInterval, StartAt: startAt, EndAt: endAt, Config: json.RawMessage(`{"interval_seconds":60}`), Tags: tags, TagCount: len(tags)}
}

func withLoggerID(logger Logger, value uuid.UUID) Logger        { logger.ID = value; return logger }
func withLoggerEnabled(logger Logger, value bool) Logger        { logger.Enabled = value; return logger }
func withLoggerMode(logger Logger, value Mode) Logger           { logger.Mode = value; return logger }
func withLoggerStart(logger Logger, value time.Time) Logger     { logger.StartAt = value; return logger }
func withLoggerTags(logger Logger, value []TagReference) Logger { logger.Tags = value; return logger }
func withLoggerConfig(logger Logger, value json.RawMessage) Logger {
	logger.Config = value
	return logger
}

func awaitSchedulerTimer(t *testing.T, clock *manualSchedulerClock) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if clock.activeTimers() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("scheduler timer was not created")
}

func awaitWrittenBatch(t *testing.T, batches <-chan RawBatch) RawBatch {
	t.Helper()
	select {
	case batch := <-batches:
		return batch
	case <-time.After(time.Second):
		t.Fatal("scheduler did not write a batch")
		return RawBatch{}
	}
}
