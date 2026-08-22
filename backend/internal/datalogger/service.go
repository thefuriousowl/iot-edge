package datalogger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultPerPage = 20
	maxPerPage     = 100
)

var (
	ErrRepositoryRequired        = errors.New("data logger repository is required")
	ErrHistoryRepositoryRequired = errors.New("data logger history repository is required")
)

type CreateInput struct {
	Name        string
	Description *string
	Enabled     *bool
	Timezone    string
	Mode        Mode
	StartAt     time.Time
	EndAt       *time.Time
	Config      json.RawMessage
	TagIDs      []uuid.UUID
}

type OptionalString struct {
	Set   bool
	Value *string
}

type OptionalTime struct {
	Set   bool
	Value *time.Time
}

type UpdateInput struct {
	Name        *string
	Description OptionalString
	Enabled     *bool
	Timezone    *string
	Mode        *Mode
	StartAt     *time.Time
	EndAt       OptionalTime
	Config      json.RawMessage
	TagIDs      *[]uuid.UUID
}

type Service struct {
	repository Repository
	history    HistoryRepository
}

func NewService(repository Repository, histories ...HistoryRepository) (*Service, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	service := &Service{repository: repository}
	if len(histories) > 0 {
		service.history = histories[0]
	}
	return service, nil
}

func (service *Service) Create(ctx context.Context, input CreateInput) (*Logger, error) {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	entity := Logger{
		Name:        input.Name,
		Description: input.Description,
		Enabled:     enabled,
		Timezone:    input.Timezone,
		Mode:        input.Mode,
		StartAt:     input.StartAt,
		EndAt:       input.EndAt,
		Config:      input.Config,
	}
	tagIDs, err := normalizeLogger(&entity, input.TagIDs)
	if err != nil {
		return nil, err
	}
	if err := service.repository.Create(ctx, &entity, tagIDs); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, entity.ID)
}

func (service *Service) Get(ctx context.Context, id uuid.UUID) (*Logger, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if input.Mode != nil && !validMode(*input.Mode) {
		return nil, ErrInvalidInput
	}
	input.Search = strings.TrimSpace(input.Search)
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = defaultPerPage
	}
	if input.PerPage > maxPerPage {
		return nil, ErrInvalidInput
	}
	return service.repository.List(ctx, input)
}

func (service *Service) ListHistory(ctx context.Context, id uuid.UUID, input RawValueListInput) (*RawValueListResult, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	if service.history == nil {
		return nil, ErrHistoryRepositoryRequired
	}
	if _, err := service.repository.Find(ctx, id); err != nil {
		return nil, err
	}
	input.LoggerID = id
	return service.history.ListValues(ctx, input)
}

func (service *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*Logger, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	current, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		current.Name = *input.Name
	}
	if input.Description.Set {
		current.Description = input.Description.Value
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.Timezone != nil {
		current.Timezone = *input.Timezone
	}
	if input.Mode != nil {
		current.Mode = *input.Mode
	}
	if input.StartAt != nil {
		current.StartAt = *input.StartAt
	}
	if input.EndAt.Set {
		current.EndAt = input.EndAt.Value
	}
	if input.Config != nil {
		current.Config = input.Config
	}
	tagIDs := make([]uuid.UUID, 0, len(current.Tags))
	for _, reference := range current.Tags {
		tagIDs = append(tagIDs, reference.ID)
	}
	if input.TagIDs != nil {
		tagIDs = append(tagIDs[:0], (*input.TagIDs)...)
	}
	tagIDs, err = normalizeLogger(current, tagIDs)
	if err != nil {
		return nil, err
	}
	if err := service.repository.Update(ctx, current, tagIDs); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrInvalidInput
	}
	return service.repository.Delete(ctx, id)
}

func normalizeLogger(entity *Logger, tagIDs []uuid.UUID) ([]uuid.UUID, error) {
	entity.Name = strings.TrimSpace(entity.Name)
	if entity.Name == "" || len(entity.Name) > 100 || !validMode(entity.Mode) || entity.StartAt.IsZero() {
		return nil, ErrInvalidInput
	}
	if entity.Description != nil {
		description := strings.TrimSpace(*entity.Description)
		if description == "" {
			entity.Description = nil
		} else {
			entity.Description = &description
		}
	}
	timezone := strings.TrimSpace(entity.Timezone)
	location, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		return nil, fmt.Errorf("%w: timezone is invalid", ErrInvalidInput)
	}
	entity.Timezone = location.String()
	entity.StartAt = entity.StartAt.UTC()
	if entity.EndAt != nil {
		endAt := entity.EndAt.UTC()
		if !endAt.After(entity.StartAt) {
			return nil, fmt.Errorf("%w: end_at must be after start_at", ErrInvalidInput)
		}
		entity.EndAt = &endAt
	}
	config, err := normalizeConfig(entity.Mode, entity.Config)
	if err != nil {
		return nil, err
	}
	entity.Config = config
	if len(tagIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one Tag is required", ErrInvalidInput)
	}
	normalizedTagIDs := make([]uuid.UUID, 0, len(tagIDs))
	seen := make(map[uuid.UUID]struct{}, len(tagIDs))
	for _, tagID := range tagIDs {
		if tagID == uuid.Nil {
			return nil, fmt.Errorf("%w: selected Tag ID is invalid", ErrInvalidInput)
		}
		if _, exists := seen[tagID]; exists {
			return nil, fmt.Errorf("%w: duplicate selected Tag %s", ErrInvalidInput, tagID)
		}
		seen[tagID] = struct{}{}
		normalizedTagIDs = append(normalizedTagIDs, tagID)
	}
	return normalizedTagIDs, nil
}

func normalizeConfig(mode Mode, raw json.RawMessage) (json.RawMessage, error) {
	switch mode {
	case ModeInterval:
		config := IntervalConfig{IntervalSeconds: DefaultIntervalSeconds}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 {
			if trimmed[0] != '{' {
				return nil, fmt.Errorf("%w: invalid interval config", ErrInvalidInput)
			}
			if err := strictDecode(raw, &config); err != nil {
				return nil, fmt.Errorf("%w: invalid interval config", ErrInvalidInput)
			}
		}
		if config.IntervalSeconds <= 0 || config.IntervalSeconds > MaxIntervalSeconds {
			return nil, fmt.Errorf("%w: interval_seconds is outside the supported range", ErrInvalidInput)
		}
		return json.Marshal(config)
	case ModeSchedule:
		var config ScheduleConfig
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil, fmt.Errorf("%w: invalid schedule config", ErrInvalidInput)
		}
		if err := strictDecode(raw, &config); err != nil {
			return nil, fmt.Errorf("%w: invalid schedule config", ErrInvalidInput)
		}
		if config.Every == 0 {
			config.Every = 1
		}
		if config.Every < 1 || config.Every > MaxScheduleEvery || !validScheduleUnit(config.Unit) {
			return nil, fmt.Errorf("%w: schedule unit and every are invalid", ErrInvalidInput)
		}
		times, err := normalizeTimes(config.Times)
		if err != nil {
			return nil, err
		}
		weekdays, err := normalizeWeekdays(config.Weekdays)
		if err != nil {
			return nil, err
		}
		switch config.Unit {
		case ScheduleMinute, ScheduleHour:
			if len(times) != 0 || len(weekdays) != 0 {
				return nil, fmt.Errorf("%w: minute/hour schedules are anchored by start_at", ErrInvalidInput)
			}
		case ScheduleDay:
			if len(times) == 0 || len(weekdays) != 0 {
				return nil, fmt.Errorf("%w: day schedules require times only", ErrInvalidInput)
			}
		case ScheduleWeek:
			if len(times) == 0 || len(weekdays) == 0 {
				return nil, fmt.Errorf("%w: week schedules require times and weekdays", ErrInvalidInput)
			}
		}
		config.Times = times
		config.Weekdays = weekdays
		return json.Marshal(config)
	default:
		return nil, ErrInvalidInput
	}
}

func strictDecode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func normalizeTimes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		parsed, err := time.Parse("15:04", value)
		if err != nil || parsed.Format("15:04") != value {
			return nil, fmt.Errorf("%w: schedule time %q is invalid", ErrInvalidInput, value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%w: duplicate schedule time %q", ErrInvalidInput, value)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeWeekdays(values []int) ([]int, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[int]struct{}, len(values))
	normalized := make([]int, 0, len(values))
	for _, value := range values {
		if value < 1 || value > 7 {
			return nil, fmt.Errorf("%w: weekday must be between 1 and 7", ErrInvalidInput)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%w: duplicate weekday %d", ErrInvalidInput, value)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Ints(normalized)
	return normalized, nil
}

func validMode(mode Mode) bool {
	return mode == ModeInterval || mode == ModeSchedule
}

func validScheduleUnit(unit ScheduleUnit) bool {
	return unit == ScheduleMinute || unit == ScheduleHour || unit == ScheduleDay || unit == ScheduleWeek
}
