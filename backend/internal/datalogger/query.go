package datalogger

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type QueryMode string
type QueryBucket string
type AggregateFunction string

const (
	QueryModeRaw       QueryMode = "raw"
	QueryModeAggregate QueryMode = "aggregate"

	QueryBucket1Minute   QueryBucket = "1m"
	QueryBucket5Minutes  QueryBucket = "5m"
	QueryBucket15Minutes QueryBucket = "15m"
	QueryBucket1Hour     QueryBucket = "1h"
	QueryBucket6Hours    QueryBucket = "6h"
	QueryBucket1Day      QueryBucket = "1d"
	QueryBucket1Week     QueryBucket = "1w"

	AggregateMin   AggregateFunction = "min"
	AggregateMax   AggregateFunction = "max"
	AggregateAvg   AggregateFunction = "avg"
	AggregateSum   AggregateFunction = "sum"
	AggregateCount AggregateFunction = "count"
	AggregateFirst AggregateFunction = "first"
	AggregateLast  AggregateFunction = "last"
)

var ErrInvalidQuery = errors.New("invalid Data Logger query")

type QueryInput struct {
	LoggerID  uuid.UUID
	TagIDs    []uuid.UUID
	From      time.Time
	To        time.Time
	Mode      QueryMode
	Bucket    QueryBucket
	Aggregate AggregateFunction
	Page      int
	PerPage   int
}

type QueryValue struct {
	TagID      uuid.UUID  `json:"tag_id"`
	DataType   string     `json:"data_type"`
	Value      any        `json:"value"`
	Quality    string     `json:"quality,omitempty"`
	Error      string     `json:"error,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	GoodCount  int64      `json:"good_count,omitempty"`
	BadCount   int64      `json:"bad_count,omitempty"`
	TotalCount int64      `json:"total_count,omitempty"`
	Supported  *bool      `json:"supported,omitempty"`
}

type QueryRow struct {
	At     time.Time             `json:"at"`
	Values map[string]QueryValue `json:"values"`
}

type QueryResult struct {
	Data       []QueryRow
	Mode       QueryMode
	Bucket     QueryBucket
	Aggregate  AggregateFunction
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type QueryRepository interface {
	Query(context.Context, QueryInput) (*QueryResult, error)
}

func (bucket QueryBucket) Seconds() int {
	switch bucket {
	case QueryBucket1Minute:
		return 60
	case QueryBucket5Minutes:
		return 5 * 60
	case QueryBucket15Minutes:
		return 15 * 60
	case QueryBucket1Hour:
		return 60 * 60
	case QueryBucket6Hours:
		return 6 * 60 * 60
	case QueryBucket1Day:
		return 24 * 60 * 60
	case QueryBucket1Week:
		return 7 * 24 * 60 * 60
	default:
		return 0
	}
}

func (value AggregateFunction) Valid() bool {
	switch value {
	case AggregateMin, AggregateMax, AggregateAvg, AggregateSum, AggregateCount, AggregateFirst, AggregateLast:
		return true
	default:
		return false
	}
}
