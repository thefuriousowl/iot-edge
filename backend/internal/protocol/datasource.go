package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidDeviceConfig     = errors.New("invalid device config")
	ErrInvalidDatasourceConfig = errors.New("invalid datasource config")
)

type DatasourceReadRequest struct {
	GatewayConfig    json.RawMessage
	DeviceConfig     json.RawMessage
	DatasourceConfig json.RawMessage
	ExecuteExclusive ExclusiveExecutor
}

type ExclusiveExecutor func(context.Context, func(context.Context) error) error

type DatasourceSample struct {
	ObservedAt time.Time       `json:"observed_at"`
	Latency    time.Duration   `json:"-"`
	Quality    string          `json:"quality"`
	Raw        []byte          `json:"-"`
	Data       json.RawMessage `json:"data"`
	Error      string          `json:"error,omitempty"`
}

type SampleEmitter func(DatasourceSample)

type DatasourceDriver interface {
	GatewayType() string
	DeviceType() string
	DatasourceTypes() []string
	NormalizeDeviceConfig(json.RawMessage) (json.RawMessage, error)
	NormalizeDatasourceConfig(string, json.RawMessage) (json.RawMessage, error)
	Preview(context.Context, DatasourceReadRequest) (DatasourceSample, error)
	Monitor(context.Context, DatasourceReadRequest, time.Duration, SampleEmitter) error
}

type Driver interface {
	GatewayDriver
	DatasourceDriver
}
