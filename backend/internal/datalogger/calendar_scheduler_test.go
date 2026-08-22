package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewCalendarSchedulerValidatesDependencies(t *testing.T) {
	t.Parallel()
	snapshots := &schedulerSnapshotReader{}
	history := &schedulerHistoryWriter{}
	tests := []struct {
		name      string
		snapshots SnapshotReader
		history   HistoryWriter
		options   []CalendarSchedulerOption
		want      error
	}{
		{name: "snapshot reader", history: history, want: ErrSnapshotReaderRequired},
		{name: "history writer", snapshots: snapshots, want: ErrHistoryWriterRequired},
		{name: "clock", snapshots: snapshots, history: history, options: []CalendarSchedulerOption{WithCalendarSchedulerClock(nil)}, want: ErrSchedulerClockRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCalendarScheduler(test.snapshots, test.history, test.options...); !errors.Is(err, test.want) {
				t.Fatalf("NewCalendarScheduler() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNextCalendarRunCalculatesAnchoredMinuteAndHourSchedules(t *testing.T) {
	t.Parallel()
	startAt := time.Date(2026, time.August, 23, 8, 0, 30, 0, time.UTC)
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	tests := []struct {
		name      string
		config    string
		threshold time.Time
		want      time.Time
	}{
		{name: "minute before start", config: `{"unit":"minute","every":15}`, threshold: startAt.Add(-time.Hour), want: startAt},
		{name: "minute between", config: `{"unit":"minute","every":15}`, threshold: startAt.Add(16 * time.Minute), want: startAt.Add(30 * time.Minute)},
		{name: "minute exact", config: `{"unit":"minute","every":15}`, threshold: startAt.Add(45 * time.Minute), want: startAt.Add(45 * time.Minute)},
		{name: "hour between", config: `{"unit":"hour","every":3}`, threshold: startAt.Add(4 * time.Hour), want: startAt.Add(6 * time.Hour)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := calendarLogger([]TagReference{tag}, "UTC", startAt, nil, test.config)
			got, exists, err := NextCalendarRun(logger, test.threshold)
			if err != nil || !exists || !got.Equal(test.want) {
				t.Fatalf("NextCalendarRun() = %s, %t, %v; want %s", got, exists, err, test.want)
			}
		})
	}
}

func TestNextCalendarRunCalculatesTimezoneAwareDailySchedule(t *testing.T) {
	t.Parallel()
	location := mustLocation(t, "Asia/Bangkok")
	startAt := time.Date(2026, time.August, 23, 7, 30, 0, 0, location)
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	logger := calendarLogger([]TagReference{tag}, location.String(), startAt, nil, `{"unit":"day","every":2,"times":["08:00","20:00"]}`)
	tests := []struct {
		name      string
		threshold time.Time
		want      time.Time
	}{
		{name: "first local time", threshold: startAt, want: time.Date(2026, time.August, 23, 8, 0, 0, 0, location)},
		{name: "later same day", threshold: time.Date(2026, time.August, 23, 9, 0, 0, 0, location), want: time.Date(2026, time.August, 23, 20, 0, 0, 0, location)},
		{name: "next active day", threshold: time.Date(2026, time.August, 23, 21, 0, 0, 0, location), want: time.Date(2026, time.August, 25, 8, 0, 0, 0, location)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, exists, err := NextCalendarRun(logger, test.threshold)
			if err != nil || !exists || !got.Equal(test.want) {
				t.Fatalf("NextCalendarRun() = %s, %t, %v; want %s", got, exists, err, test.want)
			}
		})
	}
	endAt := time.Date(2026, time.August, 23, 20, 0, 0, 0, location)
	logger.EndAt = &endAt
	got, exists, err := NextCalendarRun(logger, endAt)
	if err != nil || !exists || !got.Equal(endAt) {
		t.Errorf("inclusive end NextCalendarRun() = %s, %t, %v", got, exists, err)
	}
	if _, exists, err := NextCalendarRun(logger, endAt.Add(time.Nanosecond)); err != nil || exists {
		t.Errorf("after end NextCalendarRun() exists = %t, error = %v", exists, err)
	}
}

func TestNextCalendarRunCalculatesAnchoredWeeklySchedule(t *testing.T) {
	t.Parallel()
	location := mustLocation(t, "Asia/Bangkok")
	startAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, location)
	tag := TagReference{ID: uuid.New(), DataType: "uint16", Enabled: true}
	logger := calendarLogger([]TagReference{tag}, location.String(), startAt, nil, `{"unit":"week","every":2,"times":["09:00","18:00"],"weekdays":[1,5]}`)
	tests := []struct {
		name      string
		threshold time.Time
		want      time.Time
	}{
		{name: "start Monday skips earlier time", threshold: startAt, want: time.Date(2026, time.August, 24, 18, 0, 0, 0, location)},
		{name: "Friday same active week", threshold: time.Date(2026, time.August, 25, 0, 0, 0, 0, location), want: time.Date(2026, time.August, 28, 9, 0, 0, 0, location)},
		{name: "next active week", threshold: time.Date(2026, time.August, 28, 19, 0, 0, 0, location), want: time.Date(2026, time.September, 7, 9, 0, 0, 0, location)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, exists, err := NextCalendarRun(logger, test.threshold)
			if err != nil || !exists || !got.Equal(test.want) {
				t.Fatalf("NextCalendarRun() = %s, %t, %v; want %s", got, exists, err, test.want)
			}
		})
	}
}

func TestNextCalendarRunHandlesDSTGapsAndRepeatedTimes(t *testing.T) {
	t.Parallel()
	location := mustLocation(t, "America/New_York")
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	springStart := time.Date(2026, time.March, 7, 0, 0, 0, 0, location)
	spring := calendarLogger([]TagReference{tag}, location.String(), springStart, nil, `{"unit":"day","times":["02:30"]}`)
	threshold := time.Date(2026, time.March, 7, 3, 0, 0, 0, location)
	got, exists, err := NextCalendarRun(spring, threshold)
	want := time.Date(2026, time.March, 9, 2, 30, 0, 0, location)
	if err != nil || !exists || !got.Equal(want) {
		t.Fatalf("spring gap NextCalendarRun() = %s, %t, %v; want %s", got, exists, err, want)
	}

	fallStart := time.Date(2026, time.October, 31, 0, 0, 0, 0, location)
	fall := calendarLogger([]TagReference{tag}, location.String(), fallStart, nil, `{"unit":"day","times":["01:30"]}`)
	first, exists, err := NextCalendarRun(fall, time.Date(2026, time.November, 1, 0, 0, 0, 0, location))
	if err != nil || !exists || first.In(location).Day() != 1 || first.In(location).Hour() != 1 || first.In(location).Minute() != 30 {
		t.Fatalf("fall repeat first occurrence = %s, %t, %v", first, exists, err)
	}
	next, exists, err := NextCalendarRun(fall, first.Add(time.Nanosecond))
	if err != nil || !exists || next.In(location).Day() != 2 {
		t.Errorf("fall repeat was scheduled more than once: next = %s, %t, %v", next, exists, err)
	}
}

func TestCalendarSchedulerValidatesLoggerAndCapture(t *testing.T) {
	t.Parallel()
	startAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	endAt := startAt.Add(24 * time.Hour)
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	valid := calendarLogger([]TagReference{tag}, "UTC", startAt, &endAt, `{"unit":"day","times":["08:00"]}`)
	tests := []struct {
		name   string
		logger Logger
		at     time.Time
		want   error
	}{
		{name: "nil ID", logger: withLoggerID(valid, uuid.Nil), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "disabled", logger: withLoggerEnabled(valid, false), at: startAt, want: ErrCalendarLoggerDisabled},
		{name: "interval mode", logger: withLoggerMode(valid, ModeInterval), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "zero start", logger: withLoggerStart(valid, time.Time{}), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "no Tags", logger: withLoggerTags(valid, nil), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "bad Tag", logger: withLoggerTags(valid, []TagReference{{ID: tag.ID, DataType: "string"}}), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "bad timezone", logger: withLoggerTimezone(valid, "Moon/Base"), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "bad config", logger: withLoggerConfig(valid, json.RawMessage(`{"unit":"day"}`)), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "overflow config", logger: withLoggerConfig(valid, json.RawMessage(`{"unit":"hour","every":2562048}`)), at: startAt, want: ErrInvalidCalendarLogger},
		{name: "before start", logger: valid, at: startAt.Add(-time.Nanosecond), want: ErrInvalidCalendarLogger},
		{name: "after end", logger: valid, at: endAt.Add(time.Nanosecond), want: ErrInvalidCalendarLogger},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler := newCalendarScheduler(t, &schedulerSnapshotReader{}, &schedulerHistoryWriter{})
			if err := scheduler.Capture(context.Background(), test.logger, test.at); !errors.Is(err, test.want) {
				t.Fatalf("Capture() error = %v, want %v", err, test.want)
			}
		})
	}
	history := &schedulerHistoryWriter{}
	scheduler := newCalendarScheduler(t, &schedulerSnapshotReader{}, history)
	if err := scheduler.Capture(context.Background(), valid, startAt); err != nil || len(history.batches) != 1 {
		t.Errorf("Capture(valid) error = %v, batches = %d", err, len(history.batches))
	}
}

func TestCalendarSchedulerRunsThroughInclusiveEnd(t *testing.T) {
	startAt := time.Date(2026, time.August, 23, 7, 0, 0, 0, time.UTC)
	firstAt := startAt.Add(time.Hour)
	endAt := firstAt.Add(24 * time.Hour)
	clock := newManualSchedulerClock(firstAt.Add(-time.Minute))
	tag := TagReference{ID: uuid.New(), DataType: "float64", Enabled: true}
	snapshots := &schedulerSnapshotReader{values: map[uuid.UUID]SnapshotValue{tag.ID: {ObservedAt: startAt, DataType: "float64", Value: 1.0, Quality: RawQualityGood}}}
	historyFailure := errors.New("temporary history failure")
	history := &schedulerHistoryWriter{errorsAt: map[time.Time]error{firstAt: historyFailure}, written: make(chan RawBatch, 3)}
	reported := make(chan error, 1)
	scheduler, err := NewCalendarScheduler(snapshots, history, WithCalendarSchedulerClock(clock), WithCalendarSchedulerErrorHandler(func(_ uuid.UUID, _ time.Time, err error) { reported <- err }))
	if err != nil {
		t.Fatalf("NewCalendarScheduler() error = %v", err)
	}
	logger := calendarLogger([]TagReference{tag}, "UTC", startAt, &endAt, `{"unit":"day","times":["08:00"]}`)
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(context.Background(), logger) }()
	awaitSchedulerTimer(t, clock)
	clock.Advance(firstAt)
	if batch := awaitWrittenBatch(t, history.written); !batch.BatchAt.Equal(firstAt) {
		t.Errorf("first batch = %s, want %s", batch.BatchAt, firstAt)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, historyFailure) {
			t.Errorf("reported error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("calendar history failure was not reported")
	}
	awaitSchedulerTimer(t, clock)
	clock.Advance(endAt)
	if batch := awaitWrittenBatch(t, history.written); !batch.BatchAt.Equal(endAt) {
		t.Errorf("end batch = %s, want %s", batch.BatchAt, endAt)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("calendar scheduler did not stop after end")
	}
}

func TestCalendarSchedulerSkipsLateOccurrenceWithoutBackfill(t *testing.T) {
	startAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	clock := newManualSchedulerClock(startAt.Add(-time.Second))
	tag := TagReference{ID: uuid.New(), DataType: "bool", Enabled: true}
	history := &schedulerHistoryWriter{written: make(chan RawBatch, 2)}
	scheduler, err := NewCalendarScheduler(&schedulerSnapshotReader{}, history, WithCalendarSchedulerClock(clock))
	if err != nil {
		t.Fatalf("NewCalendarScheduler() error = %v", err)
	}
	logger := calendarLogger([]TagReference{tag}, "UTC", startAt, nil, `{"unit":"minute"}`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx, logger) }()
	awaitSchedulerTimer(t, clock)
	clock.Advance(startAt.Add(2 * time.Second))
	awaitSchedulerTimer(t, clock)
	select {
	case batch := <-history.written:
		t.Fatalf("late occurrence was backfilled: %#v", batch)
	default:
	}
	nextAt := startAt.Add(time.Minute)
	clock.Advance(nextAt)
	if batch := awaitWrittenBatch(t, history.written); !batch.BatchAt.Equal(nextAt) {
		t.Errorf("next batch = %s, want %s", batch.BatchAt, nextAt)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("calendar scheduler did not stop")
	}
}

func newCalendarScheduler(t *testing.T, snapshots SnapshotReader, history HistoryWriter) *CalendarScheduler {
	t.Helper()
	scheduler, err := NewCalendarScheduler(snapshots, history)
	if err != nil {
		t.Fatalf("NewCalendarScheduler() error = %v", err)
	}
	return scheduler
}

func calendarLogger(tags []TagReference, timezone string, startAt time.Time, endAt *time.Time, config string) Logger {
	return Logger{ID: uuid.New(), Name: "Calendar", Enabled: true, Timezone: timezone, Mode: ModeSchedule, StartAt: startAt, EndAt: endAt, Config: json.RawMessage(config), Tags: tags, TagCount: len(tags)}
}

func withLoggerTimezone(logger Logger, value string) Logger { logger.Timezone = value; return logger }

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q) error = %v", name, err)
	}
	return location
}
