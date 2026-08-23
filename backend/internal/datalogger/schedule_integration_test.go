package datalogger

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDataLoggerRuntimeRestartSchedulePolicies_Integration(t *testing.T) {
	t.Run("interval skips offline runs and captures inclusive end", func(t *testing.T) {
		startAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
		endAt := startAt.Add(4 * time.Minute)
		tags := []TagReference{
			{ID: uuid.New(), Name: "Power", DataType: "float64", Enabled: true},
			{ID: uuid.New(), Name: "Status", DataType: "uint16", Enabled: true},
		}
		logger := Logger{
			ID: uuid.New(), Name: "Restarted interval", Enabled: true, Timezone: "UTC", Mode: ModeInterval,
			StartAt: startAt, EndAt: &endAt, Config: json.RawMessage(`{"interval_seconds":60}`), Tags: tags, TagCount: len(tags),
		}
		repository := &runtimeTestRepository{loggers: []Logger{logger}}
		snapshots := &schedulerSnapshotReader{values: map[uuid.UUID]SnapshotValue{
			tags[0].ID: {ObservedAt: startAt.Add(10 * time.Second), DataType: "float64", Value: float64(12.5), Quality: RawQualityGood},
			tags[1].ID: {ObservedAt: startAt.Add(20 * time.Second), DataType: "uint16", Value: uint16(7), Quality: RawQualityGood},
		}}
		history := &schedulerHistoryWriter{written: make(chan RawBatch, 4)}
		clock := newManualSchedulerClock(startAt.Add(30 * time.Second))

		firstRuntime := newScheduleIntegrationRuntime(t, repository, snapshots, history, clock)
		if err := firstRuntime.Start(context.Background()); err != nil {
			t.Fatalf("first Start() error = %v", err)
		}
		t.Cleanup(firstRuntime.Stop)
		awaitSchedulerTimer(t, clock)
		firstAt := startAt.Add(time.Minute)
		clock.Advance(firstAt)
		assertSynchronizedBatch(t, awaitWrittenBatch(t, history.written), logger.ID, firstAt, tags, []any{float64(12.5), uint16(7)})
		firstRuntime.Stop()

		clock.Advance(startAt.Add(3*time.Minute + 30*time.Second))
		secondRuntime := newScheduleIntegrationRuntime(t, repository, snapshots, history, clock)
		if err := secondRuntime.Start(context.Background()); err != nil {
			t.Fatalf("restart Start() error = %v", err)
		}
		t.Cleanup(secondRuntime.Stop)
		awaitSchedulerTimer(t, clock)
		select {
		case batch := <-history.written:
			t.Fatalf("restart backfilled an offline interval: %#v", batch)
		default:
		}
		clock.Advance(endAt)
		assertSynchronizedBatch(t, awaitWrittenBatch(t, history.written), logger.ID, endAt, tags, []any{float64(12.5), uint16(7)})
		secondRuntime.Stop()

		batches := scheduleIntegrationBatches(history)
		if len(batches) != 2 || !batches[0].BatchAt.Equal(firstAt) || !batches[1].BatchAt.Equal(endAt) {
			t.Fatalf("interval batches = %#v, want only %s and %s", batches, firstAt, endAt)
		}
	})

	t.Run("calendar restart skips repeated DST occurrence", func(t *testing.T) {
		location := mustLocation(t, "America/New_York")
		startAt := time.Date(2026, time.October, 31, 0, 0, 0, 0, location)
		endAt := time.Date(2026, time.November, 2, 1, 30, 0, 0, location)
		tags := []TagReference{
			{ID: uuid.New(), Name: "Thermal energy", DataType: "float64", Enabled: true},
			{ID: uuid.New(), Name: "Electrical power", DataType: "float64", Enabled: true},
		}
		logger := Logger{
			ID: uuid.New(), Name: "DST calendar", Enabled: true, Timezone: location.String(), Mode: ModeSchedule,
			StartAt: startAt, EndAt: &endAt, Config: json.RawMessage(`{"unit":"day","every":1,"times":["01:30"]}`), Tags: tags, TagCount: len(tags),
		}
		firstAt, exists, err := NextCalendarRun(logger, time.Date(2026, time.November, 1, 0, 0, 0, 0, location))
		if err != nil || !exists {
			t.Fatalf("NextCalendarRun(first fold) = %s, %t, %v", firstAt, exists, err)
		}
		repository := &runtimeTestRepository{loggers: []Logger{logger}}
		snapshots := &schedulerSnapshotReader{values: map[uuid.UUID]SnapshotValue{
			tags[0].ID: {ObservedAt: firstAt.Add(-20 * time.Second), DataType: "float64", Value: float64(30), Quality: RawQualityGood},
			tags[1].ID: {ObservedAt: firstAt.Add(-10 * time.Second), DataType: "float64", Value: float64(10), Quality: RawQualityGood},
		}}
		history := &schedulerHistoryWriter{written: make(chan RawBatch, 4)}
		clock := newManualSchedulerClock(firstAt.Add(-30 * time.Minute))

		firstRuntime := newScheduleIntegrationRuntime(t, repository, snapshots, history, clock)
		if err := firstRuntime.Start(context.Background()); err != nil {
			t.Fatalf("first Start() error = %v", err)
		}
		t.Cleanup(firstRuntime.Stop)
		awaitSchedulerTimer(t, clock)
		clock.Advance(firstAt)
		assertSynchronizedBatch(t, awaitWrittenBatch(t, history.written), logger.ID, firstAt, tags, []any{float64(30), float64(10)})
		firstRuntime.Stop()

		clock.Advance(firstAt.Add(90 * time.Minute))
		secondRuntime := newScheduleIntegrationRuntime(t, repository, snapshots, history, clock)
		if err := secondRuntime.Start(context.Background()); err != nil {
			t.Fatalf("restart Start() error = %v", err)
		}
		t.Cleanup(secondRuntime.Stop)
		awaitSchedulerTimer(t, clock)
		select {
		case batch := <-history.written:
			t.Fatalf("restart captured the repeated DST occurrence: %#v", batch)
		default:
		}
		clock.Advance(endAt)
		assertSynchronizedBatch(t, awaitWrittenBatch(t, history.written), logger.ID, endAt.UTC(), tags, []any{float64(30), float64(10)})
		secondRuntime.Stop()

		batches := scheduleIntegrationBatches(history)
		if len(batches) != 2 || !batches[0].BatchAt.Equal(firstAt) || !batches[1].BatchAt.Equal(endAt) {
			t.Fatalf("calendar batches = %#v, want first fold %s and inclusive end %s", batches, firstAt, endAt)
		}
		if firstAt.In(location).Hour() != 1 || firstAt.In(location).Minute() != 30 || batches[1].BatchAt.In(location).Day() != 2 {
			t.Errorf("calendar local batch times = %s / %s", firstAt.In(location), batches[1].BatchAt.In(location))
		}
	})
}

func newScheduleIntegrationRuntime(
	t *testing.T,
	repository RuntimeRepository,
	snapshots SnapshotReader,
	history HistoryWriter,
	clock SchedulerClock,
) *Runtime {
	t.Helper()
	interval, err := NewIntervalScheduler(snapshots, history, WithIntervalSchedulerClock(clock))
	if err != nil {
		t.Fatalf("NewIntervalScheduler() error = %v", err)
	}
	calendar, err := NewCalendarScheduler(snapshots, history, WithCalendarSchedulerClock(clock))
	if err != nil {
		t.Fatalf("NewCalendarScheduler() error = %v", err)
	}
	runtime, err := NewRuntime(
		repository,
		snapshots,
		history,
		WithRuntimeReconcileInterval(0),
		WithRuntimeRunners(interval, calendar),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime
}

func assertSynchronizedBatch(
	t *testing.T,
	batch RawBatch,
	loggerID uuid.UUID,
	batchAt time.Time,
	tags []TagReference,
	wantValues []any,
) {
	t.Helper()
	if batch.LoggerID != loggerID || !batch.BatchAt.Equal(batchAt) || len(batch.Samples) != len(tags) {
		t.Fatalf("batch = %#v, want Logger %s at %s with %d samples", batch, loggerID, batchAt, len(tags))
	}
	for index, sample := range batch.Samples {
		if sample.TagID != tags[index].ID || sample.DataType != tags[index].DataType || sample.Quality != RawQualityGood || !reflect.DeepEqual(sample.Value, wantValues[index]) {
			t.Errorf("sample %d = %#v, want Tag %s value %#v", index, sample, tags[index].ID, wantValues[index])
		}
	}
}

func scheduleIntegrationBatches(history *schedulerHistoryWriter) []RawBatch {
	history.mu.Lock()
	defer history.mu.Unlock()
	return append([]RawBatch(nil), history.batches...)
}
