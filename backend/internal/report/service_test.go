package report

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

type fakeRepository struct {
	entity *Report
	list   *ListResult
	err    error
}

func (repository *fakeRepository) Create(_ context.Context, entity *Report) error {
	repository.entity = entity
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	return repository.err
}
func (repository *fakeRepository) Find(_ context.Context, id uuid.UUID) (*Report, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	if repository.entity == nil || repository.entity.ID != id {
		return nil, ErrNotFound
	}
	return repository.entity, nil
}
func (repository *fakeRepository) List(_ context.Context, _ ListInput) (*ListResult, error) {
	if repository.list == nil {
		repository.list = &ListResult{}
	}
	return repository.list, repository.err
}
func (repository *fakeRepository) Update(_ context.Context, entity *Report) error {
	repository.entity = entity
	return repository.err
}
func (repository *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	if repository.entity == nil || repository.entity.ID != id {
		return ErrNotFound
	}
	repository.entity = nil
	return repository.err
}

type fakeLoggerRepository struct {
	logger *datalogger.Logger
	err    error
}

func (repository *fakeLoggerRepository) Find(_ context.Context, id uuid.UUID) (*datalogger.Logger, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	if repository.logger == nil || repository.logger.ID != id {
		return nil, datalogger.ErrLoggerNotFound
	}
	return repository.logger, nil
}

type fakeQueryRepository struct {
	inputs []datalogger.QueryInput
	result func(datalogger.QueryInput) *datalogger.QueryResult
	err    error
}

func (repository *fakeQueryRepository) Query(_ context.Context, input datalogger.QueryInput) (*datalogger.QueryResult, error) {
	repository.inputs = append(repository.inputs, input)
	if repository.err != nil {
		return nil, repository.err
	}
	if repository.result != nil {
		return repository.result(input), nil
	}
	return &datalogger.QueryResult{Page: input.Page, PerPage: input.PerPage}, nil
}

func TestServiceCreatesNormalizedPerColumnReport(t *testing.T) {
	loggerID, numericID, boolID := uuid.New(), uuid.New(), uuid.New()
	repository := &fakeRepository{}
	service, err := NewService(repository, &fakeLoggerRepository{logger: &datalogger.Logger{ID: loggerID, Tags: []datalogger.TagReference{{ID: numericID, Name: "Power", DataType: "float64"}, {ID: boolID, Name: "Running", DataType: "bool"}}}}, &fakeQueryRepository{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	description := "  Shift report  "
	created, err := service.Create(context.Background(), SaveInput{Name: "  Energy shift  ", Description: &description, LoggerID: loggerID, Timezone: "Asia/Bangkok", Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket1Hour, Columns: []ColumnInput{{TagID: numericID, Name: "Demand kW", Aggregate: datalogger.AggregateMax}, {TagID: boolID, Name: "Running samples", Aggregate: datalogger.AggregateCount}}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name != "Energy shift" || created.Description == nil || *created.Description != "Shift report" || created.ColumnCount != 2 || created.Columns[0].Position != 0 || created.Columns[1].Aggregate != datalogger.AggregateCount {
		t.Fatalf("created = %#v", created)
	}
	_, err = service.Create(context.Background(), SaveInput{Name: "Duplicate alias", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []ColumnInput{{TagID: numericID, Name: "Value"}, {TagID: boolID, Name: " value "}}})
	if !errors.Is(err, ErrColumnNameExists) {
		t.Errorf("duplicate alias error = %v", err)
	}
	_, err = service.Create(context.Background(), SaveInput{Name: "Invalid bool", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket1Minute, Columns: []ColumnInput{{TagID: boolID, Name: "State", Aggregate: datalogger.AggregateAvg}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bool AVG error = %v", err)
	}
}

func TestServiceQueriesAndExportsAllPages(t *testing.T) {
	loggerID, reportID, numericID, boolID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	entity := &Report{ID: reportID, Name: "Plant report", LoggerID: loggerID, Timezone: "Asia/Bangkok", Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket1Hour, Columns: []Column{{TagID: numericID, Name: "Demand, kW", Aggregate: datalogger.AggregateAvg}, {TagID: boolID, Name: "Samples", Aggregate: datalogger.AggregateCount}}}
	from := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	queryRepository := &fakeQueryRepository{result: func(input datalogger.QueryInput) *datalogger.QueryResult {
		value := float64(input.Page * 10)
		return &datalogger.QueryResult{Data: []datalogger.QueryRow{{At: from.Add(time.Duration(input.Page-1) * time.Hour), Values: map[string]datalogger.QueryValue{numericID.String(): {TagID: numericID, Value: value}, boolID.String(): {TagID: boolID, Value: float64(input.Page)}}}}, Page: input.Page, PerPage: input.PerPage, Total: 2, TotalPages: 2}
	}}
	service, _ := NewService(&fakeRepository{entity: entity}, &fakeLoggerRepository{}, queryRepository)
	result, err := service.Query(context.Background(), reportID, QueryInput{From: from, To: from.Add(24 * time.Hour), Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(result.Data) != 1 || queryRepository.inputs[0].Aggregates[numericID] != datalogger.AggregateAvg || queryRepository.inputs[0].Aggregates[boolID] != datalogger.AggregateCount {
		t.Fatalf("query = %#v, input = %#v", result, queryRepository.inputs[0])
	}
	queryRepository.inputs = nil
	var output bytes.Buffer
	if err := service.ExportCSV(context.Background(), reportID, QueryInput{From: from, To: from.Add(24 * time.Hour)}, &output); err != nil {
		t.Fatalf("ExportCSV() error = %v", err)
	}
	want := "timestamp,\"Demand, kW\",Samples\n2026-08-23T07:00:00+07:00,10,1\n2026-08-23T08:00:00+07:00,20,2\n"
	if output.String() != want {
		t.Errorf("CSV = %q, want %q", output.String(), want)
	}
	if len(queryRepository.inputs) != 2 || queryRepository.inputs[0].PerPage != exportPerPage || queryRepository.inputs[1].Page != 2 {
		t.Errorf("export inputs = %#v", queryRepository.inputs)
	}
}

func TestServiceRejectsInvalidDependenciesAndQueries(t *testing.T) {
	if _, err := NewService(nil, &fakeLoggerRepository{}, &fakeQueryRepository{}); !errors.Is(err, ErrRepositoryRequired) {
		t.Errorf("nil repository error = %v", err)
	}
	if _, err := NewService(&fakeRepository{}, nil, &fakeQueryRepository{}); !errors.Is(err, ErrLoggerRequired) {
		t.Errorf("nil logger error = %v", err)
	}
	if _, err := NewService(&fakeRepository{}, &fakeLoggerRepository{}, nil); !errors.Is(err, ErrQueryRequired) {
		t.Errorf("nil query error = %v", err)
	}
	service, _ := NewService(&fakeRepository{entity: &Report{ID: uuid.New()}}, &fakeLoggerRepository{}, &fakeQueryRepository{})
	if _, err := service.Query(context.Background(), uuid.New(), QueryInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("invalid query error = %v", err)
	}
	if _, err := service.List(context.Background(), ListInput{PerPage: 101}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("invalid list error = %v", err)
	}
	if got := csvCell(datalogger.QueryValue{Value: 5.5, Quality: "bad"}); got != "" {
		t.Errorf("bad csv cell = %q", got)
	}
	if got := strings.TrimSpace(csvCell(datalogger.QueryValue{Value: true})); got != "true" {
		t.Errorf("bool csv cell = %q", got)
	}
}
