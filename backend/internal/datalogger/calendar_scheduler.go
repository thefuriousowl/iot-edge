package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCalendarLogger  = errors.New("invalid calendar Data Logger")
	ErrCalendarLoggerDisabled = errors.New("calendar Data Logger is disabled")
)

const calendarRunLatenessTolerance = time.Second

type CalendarSchedulerOption func(*CalendarScheduler)

type CalendarScheduler struct {
	snapshots SnapshotReader
	history   HistoryWriter
	clock     SchedulerClock
	onError   func(uuid.UUID, time.Time, error)
}

type calendarDefinition struct {
	config   ScheduleConfig
	location *time.Location
	times    []calendarTime
}

type calendarTime struct {
	hour   int
	minute int
}

func NewCalendarScheduler(snapshots SnapshotReader, history HistoryWriter, options ...CalendarSchedulerOption) (*CalendarScheduler, error) {
	if snapshots == nil {
		return nil, ErrSnapshotReaderRequired
	}
	if history == nil {
		return nil, ErrHistoryWriterRequired
	}
	scheduler := &CalendarScheduler{snapshots: snapshots, history: history, clock: realSchedulerClock{}, onError: func(uuid.UUID, time.Time, error) {}}
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

func WithCalendarSchedulerClock(clock SchedulerClock) CalendarSchedulerOption {
	return func(scheduler *CalendarScheduler) { scheduler.clock = clock }
}

func WithCalendarSchedulerErrorHandler(handler func(uuid.UUID, time.Time, error)) CalendarSchedulerOption {
	return func(scheduler *CalendarScheduler) {
		if handler != nil {
			scheduler.onError = handler
		}
	}
}

func (scheduler *CalendarScheduler) Run(ctx context.Context, logger Logger) error {
	if _, err := validateCalendarLogger(logger); err != nil {
		return err
	}
	threshold := scheduler.clock.Now().UTC()
	for {
		scheduledAt, exists, err := NextCalendarRun(logger, threshold)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if err := waitForSchedule(ctx, scheduler.clock, scheduledAt); err != nil {
			return err
		}
		now := scheduler.clock.Now().UTC()
		if now.Sub(scheduledAt) > calendarRunLatenessTolerance {
			current, hasCurrent, currentErr := NextCalendarRun(logger, now)
			if currentErr != nil {
				return currentErr
			}
			if !hasCurrent {
				return nil
			}
			if !current.Equal(now) {
				threshold = now
				continue
			}
			scheduledAt = current
		}
		if err := scheduler.Capture(ctx, logger, scheduledAt); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			scheduler.onError(logger.ID, scheduledAt, err)
		}
		threshold = scheduledAt.Add(time.Nanosecond)
	}
}

func (scheduler *CalendarScheduler) Capture(ctx context.Context, logger Logger, batchAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := validateCalendarLogger(logger); err != nil {
		return err
	}
	if batchAt.IsZero() {
		return ErrInvalidCalendarLogger
	}
	batchAt = batchAt.UTC()
	if batchAt.Before(logger.StartAt.UTC()) || logger.EndAt != nil && batchAt.After(logger.EndAt.UTC()) {
		return ErrInvalidCalendarLogger
	}
	return captureLatestSnapshot(ctx, scheduler.snapshots, scheduler.history, logger, batchAt)
}

func NextCalendarRun(logger Logger, threshold time.Time) (time.Time, bool, error) {
	definition, err := validateCalendarLogger(logger)
	if err != nil {
		return time.Time{}, false, err
	}
	threshold = threshold.UTC()
	if threshold.Before(logger.StartAt.UTC()) {
		threshold = logger.StartAt.UTC()
	}
	var next time.Time
	switch definition.config.Unit {
	case ScheduleMinute:
		next = nextIntervalRun(logger.StartAt.UTC(), time.Duration(definition.config.Every)*time.Minute, threshold)
	case ScheduleHour:
		next = nextIntervalRun(logger.StartAt.UTC(), time.Duration(definition.config.Every)*time.Hour, threshold)
	case ScheduleDay:
		next, err = nextDailyRun(logger.StartAt.UTC(), threshold, definition)
	case ScheduleWeek:
		next, err = nextWeeklyRun(logger.StartAt.UTC(), threshold, definition)
	default:
		err = ErrInvalidCalendarLogger
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if next.IsZero() || logger.EndAt != nil && next.After(logger.EndAt.UTC()) {
		return time.Time{}, false, nil
	}
	return next.UTC(), true, nil
}

func validateCalendarLogger(logger Logger) (calendarDefinition, error) {
	if logger.ID == uuid.Nil || logger.Mode != ModeSchedule || logger.StartAt.IsZero() || len(logger.Tags) == 0 {
		return calendarDefinition{}, ErrInvalidCalendarLogger
	}
	if !logger.Enabled {
		return calendarDefinition{}, ErrCalendarLoggerDisabled
	}
	if logger.EndAt != nil && !logger.EndAt.After(logger.StartAt) {
		return calendarDefinition{}, ErrInvalidCalendarLogger
	}
	location, err := time.LoadLocation(logger.Timezone)
	if err != nil || logger.Timezone == "" {
		return calendarDefinition{}, ErrInvalidCalendarLogger
	}
	normalized, err := normalizeConfig(ModeSchedule, logger.Config)
	if err != nil {
		return calendarDefinition{}, fmt.Errorf("%w: %v", ErrInvalidCalendarLogger, err)
	}
	var config ScheduleConfig
	if err := json.Unmarshal(normalized, &config); err != nil || config.Every < 1 || config.Every > MaxScheduleEvery {
		return calendarDefinition{}, ErrInvalidCalendarLogger
	}
	times := make([]calendarTime, 0, len(config.Times))
	for _, value := range config.Times {
		parsed, parseErr := time.Parse("15:04", value)
		if parseErr != nil {
			return calendarDefinition{}, ErrInvalidCalendarLogger
		}
		times = append(times, calendarTime{hour: parsed.Hour(), minute: parsed.Minute()})
	}
	for _, reference := range logger.Tags {
		if reference.ID == uuid.Nil || !validHistoryDataType(reference.DataType) {
			return calendarDefinition{}, ErrInvalidCalendarLogger
		}
	}
	return calendarDefinition{config: config, location: location, times: times}, nil
}

func nextDailyRun(startAt, threshold time.Time, definition calendarDefinition) (time.Time, error) {
	startDate := civilDate(startAt.In(definition.location))
	targetDate := civilDate(threshold.In(definition.location))
	daysSinceStart := civilDaysBetween(startDate, targetDate)
	if daysSinceStart < 0 {
		daysSinceStart = 0
	}
	cycle := nextMultiple(daysSinceStart, definition.config.Every)
	for attempts := 0; attempts < 8; attempts++ {
		date := startDate.AddDate(0, 0, cycle)
		for _, configured := range definition.times {
			candidate, exists := resolveLocalOccurrence(date, configured, definition.location)
			if exists && !candidate.Before(startAt) && !candidate.Before(threshold) {
				return candidate.UTC(), nil
			}
		}
		cycle += definition.config.Every
	}
	return time.Time{}, ErrInvalidCalendarLogger
}

func nextWeeklyRun(startAt, threshold time.Time, definition calendarDefinition) (time.Time, error) {
	startLocalDate := civilDate(startAt.In(definition.location))
	startWeek := startLocalDate.AddDate(0, 0, 1-isoWeekday(startLocalDate.Weekday()))
	targetDate := civilDate(threshold.In(definition.location))
	weeksSinceStart := civilDaysBetween(startWeek, targetDate) / 7
	if weeksSinceStart < 0 {
		weeksSinceStart = 0
	}
	activeWeek := nextMultiple(weeksSinceStart, definition.config.Every)
	weekdays := append([]int(nil), definition.config.Weekdays...)
	sort.Ints(weekdays)
	for attempts := 0; attempts < 8; attempts++ {
		weekStart := startWeek.AddDate(0, 0, activeWeek*7)
		for _, weekday := range weekdays {
			date := weekStart.AddDate(0, 0, weekday-1)
			for _, configured := range definition.times {
				candidate, exists := resolveLocalOccurrence(date, configured, definition.location)
				if exists && !candidate.Before(startAt) && !candidate.Before(threshold) {
					return candidate.UTC(), nil
				}
			}
		}
		activeWeek += definition.config.Every
	}
	return time.Time{}, ErrInvalidCalendarLogger
}

func resolveLocalOccurrence(date time.Time, configured calendarTime, location *time.Location) (time.Time, bool) {
	candidate := time.Date(date.Year(), date.Month(), date.Day(), configured.hour, configured.minute, 0, 0, location)
	if !sameLocalMinute(candidate, date, configured, location) {
		return time.Time{}, false
	}
	first := candidate
	for offset := -3 * time.Hour; offset <= 3*time.Hour; offset += time.Minute {
		alternative := candidate.Add(offset)
		if alternative.Before(first) && sameLocalMinute(alternative, date, configured, location) {
			first = alternative
		}
	}
	return first.UTC(), true
}

func sameLocalMinute(candidate, date time.Time, configured calendarTime, location *time.Location) bool {
	local := candidate.In(location)
	return local.Year() == date.Year() && local.Month() == date.Month() && local.Day() == date.Day() && local.Hour() == configured.hour && local.Minute() == configured.minute
}

func civilDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func civilDaysBetween(from, to time.Time) int {
	return int(to.Sub(from) / (24 * time.Hour))
}

func nextMultiple(value, every int) int {
	remainder := value % every
	if remainder == 0 {
		return value
	}
	return value + every - remainder
}

func isoWeekday(value time.Weekday) int {
	if value == time.Sunday {
		return 7
	}
	return int(value)
}
