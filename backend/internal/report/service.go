package report

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

const (
	defaultPerPage = 20
	maxPerPage     = 100
	exportPerPage  = 500
)

type ColumnInput struct {
	TagID     uuid.UUID
	Name      string
	Aggregate datalogger.AggregateFunction
}

type SaveInput struct {
	Name        string
	Description *string
	LoggerID    uuid.UUID
	Timezone    string
	Mode        datalogger.QueryMode
	Bucket      datalogger.QueryBucket
	Columns     []ColumnInput
}

type Service struct {
	repository Repository
	loggers    LoggerRepository
	history    datalogger.QueryRepository
}

func NewService(repository Repository, loggers LoggerRepository, history datalogger.QueryRepository) (*Service, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if loggers == nil {
		return nil, ErrLoggerRequired
	}
	if history == nil {
		return nil, ErrQueryRequired
	}
	return &Service{repository: repository, loggers: loggers, history: history}, nil
}

func (service *Service) Create(ctx context.Context, input SaveInput) (*Report, error) {
	entity, err := service.normalize(ctx, uuid.Nil, input)
	if err != nil {
		return nil, err
	}
	if err := service.repository.Create(ctx, entity); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, entity.ID)
}

func (service *Service) Get(ctx context.Context, id uuid.UUID) (*Report, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if input.LoggerID != nil && *input.LoggerID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	if input.Mode != nil && *input.Mode != datalogger.QueryModeRaw && *input.Mode != datalogger.QueryModeAggregate {
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

func (service *Service) Update(ctx context.Context, id uuid.UUID, input SaveInput) (*Report, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	if _, err := service.repository.Find(ctx, id); err != nil {
		return nil, err
	}
	entity, err := service.normalize(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if err := service.repository.Update(ctx, entity); err != nil {
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

func (service *Service) Query(ctx context.Context, id uuid.UUID, input QueryInput) (*QueryResult, error) {
	entity, query, err := service.prepareQuery(ctx, id, input)
	if err != nil {
		return nil, err
	}
	result, err := service.history.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	return &QueryResult{Report: entity, Data: result.Data, Page: result.Page, PerPage: result.PerPage, Total: result.Total, TotalPages: result.TotalPages}, nil
}

func (service *Service) ExportCSV(ctx context.Context, id uuid.UUID, input QueryInput, writer io.Writer) error {
	entity, query, err := service.prepareQuery(ctx, id, input)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(entity.Timezone)
	if err != nil {
		return ErrInvalidReport
	}
	csvWriter := csv.NewWriter(writer)
	header := make([]string, 1, len(entity.Columns)+1)
	header[0] = "timestamp"
	for _, column := range entity.Columns {
		header = append(header, column.Name)
	}
	if err := csvWriter.Write(header); err != nil {
		return err
	}
	query.Page = 1
	query.PerPage = exportPerPage
	for {
		result, queryErr := service.history.Query(ctx, query)
		if queryErr != nil {
			return queryErr
		}
		for _, row := range result.Data {
			record := make([]string, 1, len(entity.Columns)+1)
			record[0] = row.At.In(location).Format(time.RFC3339)
			for _, column := range entity.Columns {
				record = append(record, csvCell(row.Values[column.TagID.String()]))
			}
			if err := csvWriter.Write(record); err != nil {
				return err
			}
		}
		if query.Page >= result.TotalPages || len(result.Data) == 0 {
			break
		}
		query.Page++
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func (service *Service) normalize(ctx context.Context, id uuid.UUID, input SaveInput) (*Report, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 100 || input.LoggerID == uuid.Nil || len(input.Columns) == 0 || len(input.Columns) > MaxColumns {
		return nil, ErrInvalidInput
	}
	description := input.Description
	if description != nil {
		value := strings.TrimSpace(*description)
		if value == "" {
			description = nil
		} else {
			description = &value
		}
	}
	timezone := strings.TrimSpace(input.Timezone)
	location, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		return nil, ErrInvalidInput
	}
	if input.Mode == datalogger.QueryModeRaw {
		input.Bucket = ""
	} else if input.Mode != datalogger.QueryModeAggregate || input.Bucket.Seconds() == 0 {
		return nil, ErrInvalidInput
	}
	logger, err := service.loggers.Find(ctx, input.LoggerID)
	if err != nil {
		return nil, err
	}
	selected := make(map[uuid.UUID]datalogger.TagReference, len(logger.Tags))
	for _, tag := range logger.Tags {
		selected[tag.ID] = tag
	}
	columns := make([]Column, 0, len(input.Columns))
	seenTags := make(map[uuid.UUID]struct{}, len(input.Columns))
	seenNames := make(map[string]struct{}, len(input.Columns))
	for position, columnInput := range input.Columns {
		tag, exists := selected[columnInput.TagID]
		if columnInput.TagID == uuid.Nil || !exists {
			return nil, ErrTagNotSelected
		}
		if _, duplicate := seenTags[columnInput.TagID]; duplicate {
			return nil, ErrInvalidInput
		}
		columnName := strings.TrimSpace(columnInput.Name)
		if columnName == "" {
			columnName = tag.Name
		}
		if len(columnName) > 100 {
			return nil, ErrInvalidInput
		}
		nameKey := strings.ToLower(columnName)
		if _, duplicate := seenNames[nameKey]; duplicate {
			return nil, ErrColumnNameExists
		}
		aggregate := columnInput.Aggregate
		if input.Mode == datalogger.QueryModeRaw {
			aggregate = ""
		} else {
			if aggregate == "" {
				aggregate = datalogger.AggregateAvg
			}
			if !aggregate.Valid() || (tag.DataType == "bool" && aggregate != datalogger.AggregateCount && aggregate != datalogger.AggregateFirst && aggregate != datalogger.AggregateLast) {
				return nil, ErrInvalidInput
			}
		}
		seenTags[columnInput.TagID] = struct{}{}
		seenNames[nameKey] = struct{}{}
		columns = append(columns, Column{ReportID: id, LoggerID: input.LoggerID, TagID: columnInput.TagID, Position: position, Name: columnName, Aggregate: aggregate})
	}
	return &Report{ID: id, Name: name, Description: description, LoggerID: input.LoggerID, Timezone: location.String(), Mode: input.Mode, Bucket: input.Bucket, Columns: columns, ColumnCount: len(columns)}, nil
}

func (service *Service) prepareQuery(ctx context.Context, id uuid.UUID, input QueryInput) (*Report, datalogger.QueryInput, error) {
	if id == uuid.Nil || input.From.IsZero() || input.To.IsZero() || !input.To.After(input.From) || input.To.Sub(input.From) > 366*24*time.Hour {
		return nil, datalogger.QueryInput{}, ErrInvalidInput
	}
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = 100
	}
	if input.PerPage > exportPerPage {
		return nil, datalogger.QueryInput{}, ErrInvalidInput
	}
	entity, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, datalogger.QueryInput{}, err
	}
	if len(entity.Columns) == 0 {
		return nil, datalogger.QueryInput{}, ErrInvalidReport
	}
	query := datalogger.QueryInput{LoggerID: entity.LoggerID, From: input.From.UTC(), To: input.To.UTC(), Mode: entity.Mode, Bucket: entity.Bucket, Page: input.Page, PerPage: input.PerPage}
	for _, column := range entity.Columns {
		query.TagIDs = append(query.TagIDs, column.TagID)
		if entity.Mode == datalogger.QueryModeAggregate {
			if query.Aggregates == nil {
				query.Aggregates = make(map[uuid.UUID]datalogger.AggregateFunction, len(entity.Columns))
			}
			query.Aggregates[column.TagID] = column.Aggregate
		}
	}
	return entity, query, nil
}

func csvCell(value datalogger.QueryValue) string {
	if value.Value == nil || value.Quality == "bad" || (value.Supported != nil && !*value.Supported) {
		return ""
	}
	switch typed := value.Value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	default:
		return fmt.Sprint(typed)
	}
}
