package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewServiceRequiresRepository(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("NewService() error = %v, want %v", err, ErrRepositoryRequired)
	}
}

func TestServiceCreatesAndNormalizesLoggerModes(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	service := newTestService(t, repository)
	tagA, tagB := repository.addTag("Power"), repository.addTag("Energy")
	description := "  Main meter  "
	disabled := false
	startAt := time.Date(2026, time.August, 23, 7, 0, 0, 0, time.FixedZone("ICT", 7*60*60))

	interval, err := service.Create(context.Background(), CreateInput{
		Name: "  Main logger  ", Description: &description, Enabled: &disabled, Timezone: "UTC", Mode: ModeInterval,
		StartAt: startAt, TagIDs: []uuid.UUID{tagB.ID, tagA.ID},
	})
	if err != nil {
		t.Fatalf("Create(interval) error = %v", err)
	}
	if interval.ID == uuid.Nil || interval.Name != "Main logger" || interval.Description == nil || *interval.Description != "Main meter" || interval.Enabled || interval.Timezone != "UTC" || !interval.StartAt.Equal(startAt.UTC()) {
		t.Errorf("interval = %#v", interval)
	}
	if string(interval.Config) != `{"interval_seconds":60}` || interval.TagCount != 2 || interval.Tags[0].ID != tagB.ID || interval.Tags[1].ID != tagA.ID {
		t.Errorf("interval config/tags = %s / %#v", interval.Config, interval.Tags)
	}

	schedule, err := service.Create(context.Background(), CreateInput{
		Name: "Weekly", Timezone: "Asia/Bangkok", Mode: ModeSchedule, StartAt: startAt,
		Config: json.RawMessage(`{"unit":"week","every":2,"times":["17:30","08:00"],"weekdays":[5,1]}`), TagIDs: []uuid.UUID{tagA.ID},
	})
	if err != nil {
		t.Fatalf("Create(schedule) error = %v", err)
	}
	if schedule.Timezone != "Asia/Bangkok" || string(schedule.Config) != `{"unit":"week","every":2,"times":["08:00","17:30"],"weekdays":[1,5]}` {
		t.Errorf("schedule = %#v", schedule)
	}
}

func TestServiceAcceptsScheduleContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{name: "minute", config: `{"unit":"minute"}`, want: `{"unit":"minute","every":1}`},
		{name: "hour", config: `{"unit":"hour","every":3}`, want: `{"unit":"hour","every":3}`},
		{name: "day", config: `{"unit":"day","times":["12:00","06:00"]}`, want: `{"unit":"day","every":1,"times":["06:00","12:00"]}`},
		{name: "week", config: `{"unit":"week","times":["09:30"],"weekdays":[7,2]}`, want: `{"unit":"week","every":1,"times":["09:30"],"weekdays":[2,7]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newMemoryRepository()
			tag := repository.addTag("Source")
			service := newTestService(t, repository)
			entity, err := service.Create(context.Background(), CreateInput{Name: test.name, Timezone: "UTC", Mode: ModeSchedule, StartAt: time.Now(), Config: json.RawMessage(test.config), TagIDs: []uuid.UUID{tag.ID}})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if string(entity.Config) != test.want {
				t.Errorf("config = %s, want %s", entity.Config, test.want)
			}
		})
	}
}

func TestServiceRejectsInvalidLoggerContracts(t *testing.T) {
	t.Parallel()
	tagID := uuid.New()
	startAt := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	endBefore := startAt.Add(-time.Second)
	tests := []struct {
		name  string
		input CreateInput
	}{
		{name: "blank name", input: validCreate(tagID, startAt, `{"interval_seconds":1}`)},
		{name: "long name", input: withName(validCreate(tagID, startAt, `{}`), strings.Repeat("x", 101))},
		{name: "bad mode", input: withMode(validCreate(tagID, startAt, `{}`), Mode("event"))},
		{name: "zero start", input: withStart(validCreate(tagID, startAt, `{}`), time.Time{})},
		{name: "bad timezone", input: withTimezone(validCreate(tagID, startAt, `{}`), "Moon/Base")},
		{name: "end before start", input: withEnd(validCreate(tagID, startAt, `{}`), &endBefore)},
		{name: "storage below minimum", input: withMaxSize(validCreate(tagID, startAt, `{}`), int64Pointer(MinStorageSizeBytes-1))},
		{name: "storage above maximum", input: withMaxSize(validCreate(tagID, startAt, `{}`), int64Pointer(MaxStorageSizeBytes+1))},
		{name: "no tags", input: withTags(validCreate(tagID, startAt, `{}`), nil)},
		{name: "nil tag", input: withTags(validCreate(tagID, startAt, `{}`), []uuid.UUID{uuid.Nil})},
		{name: "duplicate tag", input: withTags(validCreate(tagID, startAt, `{}`), []uuid.UUID{tagID, tagID})},
		{name: "interval non-object", input: validCreate(tagID, startAt, `null`)},
		{name: "interval unknown", input: validCreate(tagID, startAt, `{"seconds":1}`)},
		{name: "interval nonpositive", input: validCreate(tagID, startAt, `{"interval_seconds":0}`)},
		{name: "interval overflow", input: validCreate(tagID, startAt, `{"interval_seconds":9223372037}`)},
		{name: "multiple JSON", input: validCreate(tagID, startAt, `{} {}`)},
		{name: "schedule missing config", input: scheduleCreate(tagID, startAt, ``)},
		{name: "schedule bad unit", input: scheduleCreate(tagID, startAt, `{"unit":"month"}`)},
		{name: "schedule negative every", input: scheduleCreate(tagID, startAt, `{"unit":"minute","every":-1}`)},
		{name: "schedule overflowing every", input: scheduleCreate(tagID, startAt, `{"unit":"hour","every":2562048}`)},
		{name: "minute with times", input: scheduleCreate(tagID, startAt, `{"unit":"minute","times":["08:00"]}`)},
		{name: "hour with weekday", input: scheduleCreate(tagID, startAt, `{"unit":"hour","weekdays":[1]}`)},
		{name: "day missing times", input: scheduleCreate(tagID, startAt, `{"unit":"day"}`)},
		{name: "day with weekdays", input: scheduleCreate(tagID, startAt, `{"unit":"day","times":["08:00"],"weekdays":[1]}`)},
		{name: "week missing weekdays", input: scheduleCreate(tagID, startAt, `{"unit":"week","times":["08:00"]}`)},
		{name: "bad time", input: scheduleCreate(tagID, startAt, `{"unit":"day","times":["8:00"]}`)},
		{name: "duplicate time", input: scheduleCreate(tagID, startAt, `{"unit":"day","times":["08:00","08:00"]}`)},
		{name: "bad weekday", input: scheduleCreate(tagID, startAt, `{"unit":"week","times":["08:00"],"weekdays":[0]}`)},
		{name: "duplicate weekday", input: scheduleCreate(tagID, startAt, `{"unit":"week","times":["08:00"],"weekdays":[1,1]}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "blank name" {
				test.input.Name = " "
			}
			repository := newMemoryRepository()
			repository.tags[tagID] = TagReference{ID: tagID, Name: "Tag"}
			service := newTestService(t, repository)
			if _, err := service.Create(context.Background(), test.input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create() error = %v, want %v", err, ErrInvalidInput)
			}
		})
	}
}

func TestServiceHydratesStorageAndEnforcesUpdatedLimit(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	tag := repository.addTag("Power")
	limit := int64(100 * 1024 * 1024)
	history := &memoryHistoryRepository{storage: &StorageStats{RowCount: 12, BatchCount: 12, AverageRowBytes: 400}}
	service, err := NewService(repository, history)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	entity, err := service.Create(context.Background(), withMaxSize(validCreate(tag.ID, time.Now(), `{}`), &limit))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if entity.MaxSizeBytes == nil || *entity.MaxSizeBytes != limit || entity.Storage == nil || entity.Storage.RowCount != 12 || history.storageTagCount != 1 || history.storageLimit == nil || *history.storageLimit != limit {
		t.Fatalf("created storage = %#v, history = %#v", entity, history)
	}
	updatedLimit := int64(200 * 1024 * 1024)
	updated, err := service.Update(context.Background(), entity.ID, UpdateInput{MaxSizeBytes: OptionalInt64{Set: true, Value: &updatedLimit}})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.MaxSizeBytes == nil || *updated.MaxSizeBytes != updatedLimit || history.validatedLimit == nil || *history.validatedLimit != updatedLimit || history.enforcedLimit == nil || *history.enforcedLimit != updatedLimit {
		t.Errorf("updated storage = %#v, validated/enforced = %v/%v", updated, history.validatedLimit, history.enforcedLimit)
	}
}

func TestServiceUpdatesListsDeletesAndPropagatesErrors(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	tagA, tagB := repository.addTag("A"), repository.addTag("B")
	service := newTestService(t, repository)
	startAt := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	entity, err := service.Create(context.Background(), withName(validCreate(tagA.ID, startAt, `{}`), "Original"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	name, timezone, mode, description := " Updated ", "Asia/Bangkok", ModeSchedule, " Notes "
	config := json.RawMessage(`{"unit":"day","times":["08:00"]}`)
	tagIDs := []uuid.UUID{tagB.ID, tagA.ID}
	updated, err := service.Update(context.Background(), entity.ID, UpdateInput{
		Name: &name, Description: OptionalString{Set: true, Value: &description}, Timezone: &timezone, Mode: &mode, Config: config, TagIDs: &tagIDs,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Updated" || updated.Description == nil || *updated.Description != "Notes" || updated.Timezone != "Asia/Bangkok" || updated.Mode != ModeSchedule || updated.Tags[0].ID != tagB.ID {
		t.Errorf("updated = %#v", updated)
	}
	updated, err = service.Update(context.Background(), entity.ID, UpdateInput{Description: OptionalString{Set: true}, EndAt: OptionalTime{Set: true}})
	if err != nil || updated.Description != nil || updated.EndAt != nil {
		t.Errorf("clear nullable fields = %#v, error = %v", updated, err)
	}

	result, err := service.List(context.Background(), ListInput{Search: " Updated "})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repository.lastList.Page != 1 || repository.lastList.PerPage != 20 || repository.lastList.Search != "Updated" || len(result.Data) != 1 {
		t.Errorf("list = %#v, input = %#v", result, repository.lastList)
	}
	invalidMode := Mode("future")
	if _, err := service.List(context.Background(), ListInput{Mode: &invalidMode}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("List(invalid mode) error = %v", err)
	}
	if _, err := service.List(context.Background(), ListInput{PerPage: 101}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("List(per page) error = %v", err)
	}
	if _, err := service.Get(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Get(nil) error = %v", err)
	}
	if err := service.Delete(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Delete(nil) error = %v", err)
	}
	if err := service.Delete(context.Background(), entity.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(context.Background(), entity.ID); !errors.Is(err, ErrLoggerNotFound) {
		t.Errorf("Get(deleted) error = %v", err)
	}

	repository.err = errors.New("database unavailable")
	if _, err := service.List(context.Background(), ListInput{}); !errors.Is(err, repository.err) {
		t.Errorf("List(repository error) = %v", err)
	}
}

func TestServiceListsHistoryForExistingLogger(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	tag := repository.addTag("Power")
	history := &memoryHistoryRepository{result: &RawValueListResult{Page: 2, PerPage: 25, Total: 26, TotalPages: 2}}
	service, err := NewService(repository, history)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	entity, err := service.Create(context.Background(), withName(validCreate(tag.ID, time.Now(), `{}`), "History"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	from, to, filterTag := time.Now().Add(-time.Hour), time.Now(), tag.ID
	result, err := service.ListHistory(context.Background(), entity.ID, RawValueListInput{TagID: &filterTag, From: &from, To: &to, Page: 2, PerPage: 25})
	if err != nil || result.Total != 26 {
		t.Fatalf("ListHistory() = %#v, %v", result, err)
	}
	if history.input.LoggerID != entity.ID || history.input.TagID == nil || *history.input.TagID != tag.ID || history.input.Page != 2 || history.input.PerPage != 25 {
		t.Errorf("history input = %#v", history.input)
	}
	if _, err := service.ListHistory(context.Background(), uuid.Nil, RawValueListInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ListHistory(nil) error = %v", err)
	}
	if _, err := service.ListHistory(context.Background(), uuid.New(), RawValueListInput{}); !errors.Is(err, ErrLoggerNotFound) {
		t.Errorf("ListHistory(missing) error = %v", err)
	}
	serviceWithoutHistory := newTestService(t, repository)
	if _, err := serviceWithoutHistory.ListHistory(context.Background(), entity.ID, RawValueListInput{}); !errors.Is(err, ErrHistoryRepositoryRequired) {
		t.Errorf("ListHistory(no history repository) error = %v", err)
	}
}

func TestServiceQueriesSelectedLoggerTagsAndNormalizesDefaults(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	first := repository.addTag("Power")
	second := repository.addTag("Running")
	history := &memoryHistoryRepository{queryResult: &QueryResult{Total: 2}}
	service, err := NewService(repository, history)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	entity, err := service.Create(context.Background(), withTags(validCreate(first.ID, time.Now(), `{}`), []uuid.UUID{first.ID, second.ID}))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	to := from.Add(24 * time.Hour)
	result, err := service.QueryHistory(context.Background(), entity.ID, QueryInput{From: from, To: to, Mode: QueryModeAggregate, Bucket: QueryBucket5Minutes, Aggregate: AggregateAvg})
	if err != nil || result.Total != 2 {
		t.Fatalf("QueryHistory() = %#v, %v", result, err)
	}
	if history.queryInput.LoggerID != entity.ID || history.queryInput.Page != 1 || history.queryInput.PerPage != 100 || len(history.queryInput.TagIDs) != 2 || history.queryInput.TagIDs[0] != first.ID || !history.queryInput.From.Equal(from.UTC()) {
		t.Errorf("query input = %#v", history.queryInput)
	}
	if _, err := service.QueryHistory(context.Background(), entity.ID, QueryInput{TagIDs: []uuid.UUID{uuid.New()}, From: from, To: to}); !errors.Is(err, ErrRawTagNotSelected) {
		t.Errorf("QueryHistory(unselected Tag) error = %v", err)
	}
	invalid := []QueryInput{
		{From: from, To: from},
		{From: from, To: from.AddDate(1, 0, 2)},
		{From: from, To: to, Mode: "invalid"},
		{From: from, To: to, Mode: QueryModeAggregate, Bucket: "2m", Aggregate: AggregateAvg},
		{From: from, To: to, Mode: QueryModeAggregate, Bucket: QueryBucket1Hour, Aggregate: "median"},
		{From: from, To: to, PerPage: 501},
	}
	for _, input := range invalid {
		if _, err := service.QueryHistory(context.Background(), entity.ID, input); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("QueryHistory(%#v) error = %v", input, err)
		}
	}
}

type memoryRepository struct {
	loggers  map[uuid.UUID]Logger
	tags     map[uuid.UUID]TagReference
	lastList ListInput
	err      error
}

type memoryHistoryRepository struct {
	input           RawValueListInput
	result          *RawValueListResult
	queryInput      QueryInput
	queryResult     *QueryResult
	storage         *StorageStats
	storageTagCount int
	storageLimit    *int64
	validatedLimit  *int64
	enforcedLimit   *int64
	err             error
}

func (repository *memoryHistoryRepository) WriteBatch(context.Context, RawBatch) error {
	return repository.err
}

func (repository *memoryHistoryRepository) ListValues(_ context.Context, input RawValueListInput) (*RawValueListResult, error) {
	repository.input = input
	return repository.result, repository.err
}

func (repository *memoryHistoryRepository) Query(_ context.Context, input QueryInput) (*QueryResult, error) {
	repository.queryInput = input
	if repository.queryResult == nil {
		return &QueryResult{Mode: input.Mode, Bucket: input.Bucket, Aggregate: input.Aggregate, Page: input.Page, PerPage: input.PerPage}, repository.err
	}
	return repository.queryResult, repository.err
}

func (repository *memoryHistoryRepository) Storage(_ context.Context, _ uuid.UUID, tagCount int, maxSizeBytes *int64) (*StorageStats, error) {
	repository.storageTagCount = tagCount
	repository.storageLimit = maxSizeBytes
	if repository.storage == nil {
		return &StorageStats{AverageRowBytes: DefaultEstimatedRowBytes}, repository.err
	}
	return repository.storage, repository.err
}

func (repository *memoryHistoryRepository) EnforceStorageLimit(_ context.Context, _ uuid.UUID, maxSizeBytes *int64) error {
	repository.enforcedLimit = maxSizeBytes
	return repository.err
}

func (repository *memoryHistoryRepository) ValidateStorageLimit(_ context.Context, _ uuid.UUID, maxSizeBytes *int64) error {
	repository.validatedLimit = maxSizeBytes
	return repository.err
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{loggers: map[uuid.UUID]Logger{}, tags: map[uuid.UUID]TagReference{}}
}

func (repository *memoryRepository) addTag(name string) TagReference {
	tag := TagReference{ID: uuid.New(), Name: name, Type: "reading", DataType: "float64", Enabled: true}
	repository.tags[tag.ID] = tag
	return tag
}

func (repository *memoryRepository) Create(_ context.Context, entity *Logger, tagIDs []uuid.UUID) error {
	if repository.err != nil {
		return repository.err
	}
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	return repository.store(entity, tagIDs)
}

func (repository *memoryRepository) Find(_ context.Context, id uuid.UUID) (*Logger, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	entity, exists := repository.loggers[id]
	if !exists {
		return nil, ErrLoggerNotFound
	}
	copy := entity
	copy.Config = append(json.RawMessage(nil), entity.Config...)
	copy.Tags = append([]TagReference(nil), entity.Tags...)
	return &copy, nil
}

func (repository *memoryRepository) List(_ context.Context, input ListInput) (*ListResult, error) {
	repository.lastList = input
	if repository.err != nil {
		return nil, repository.err
	}
	data := make([]Logger, 0, len(repository.loggers))
	for _, entity := range repository.loggers {
		if input.Mode != nil && entity.Mode != *input.Mode || input.Enabled != nil && entity.Enabled != *input.Enabled || input.Search != "" && !strings.Contains(strings.ToLower(entity.Name), strings.ToLower(input.Search)) {
			continue
		}
		data = append(data, entity)
	}
	return &ListResult{Data: data, Page: input.Page, PerPage: input.PerPage, Total: int64(len(data)), TotalPages: len(data)}, nil
}

func (repository *memoryRepository) Update(_ context.Context, entity *Logger, tagIDs []uuid.UUID) error {
	if repository.err != nil {
		return repository.err
	}
	if _, exists := repository.loggers[entity.ID]; !exists {
		return ErrLoggerNotFound
	}
	return repository.store(entity, tagIDs)
}

func (repository *memoryRepository) Delete(_ context.Context, id uuid.UUID) error {
	if repository.err != nil {
		return repository.err
	}
	if _, exists := repository.loggers[id]; !exists {
		return ErrLoggerNotFound
	}
	delete(repository.loggers, id)
	return nil
}

func (repository *memoryRepository) store(entity *Logger, tagIDs []uuid.UUID) error {
	tags := make([]TagReference, 0, len(tagIDs))
	for position, tagID := range tagIDs {
		tag, exists := repository.tags[tagID]
		if !exists {
			return ErrLoggerTagNotFound
		}
		tag.Position = position
		tags = append(tags, tag)
	}
	copy := *entity
	copy.Tags = tags
	copy.TagCount = len(tags)
	copy.Config = append(json.RawMessage(nil), entity.Config...)
	repository.loggers[copy.ID] = copy
	return nil
}

func newTestService(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func validCreate(tagID uuid.UUID, startAt time.Time, config string) CreateInput {
	return CreateInput{Name: "Logger", Timezone: "UTC", Mode: ModeInterval, StartAt: startAt, Config: json.RawMessage(config), TagIDs: []uuid.UUID{tagID}}
}

func scheduleCreate(tagID uuid.UUID, startAt time.Time, config string) CreateInput {
	input := validCreate(tagID, startAt, config)
	input.Mode = ModeSchedule
	return input
}

func withName(input CreateInput, value string) CreateInput     { input.Name = value; return input }
func withMode(input CreateInput, value Mode) CreateInput       { input.Mode = value; return input }
func withStart(input CreateInput, value time.Time) CreateInput { input.StartAt = value; return input }
func withTimezone(input CreateInput, value string) CreateInput { input.Timezone = value; return input }
func withEnd(input CreateInput, value *time.Time) CreateInput  { input.EndAt = value; return input }
func withMaxSize(input CreateInput, value *int64) CreateInput {
	input.MaxSizeBytes = value
	return input
}
func withTags(input CreateInput, value []uuid.UUID) CreateInput { input.TagIDs = value; return input }

func int64Pointer(value int64) *int64 { return &value }

var _ Repository = (*memoryRepository)(nil)
var _ HistoryRepository = (*memoryHistoryRepository)(nil)
