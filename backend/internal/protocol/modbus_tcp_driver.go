package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	DefaultModbusTCPPort              = 502
	DefaultModbusTCPTimeout           = 5000
	DefaultModbusTCPRetryCount        = 3
	DefaultModbusTCPRetryDelay        = 1000
	DefaultModbusTCPReconnectInterval = 30
)

type ModbusTCPConfigInput struct {
	Host              string `json:"host"`
	Port              *int   `json:"port"`
	Timeout           *int   `json:"timeout"`
	RetryCount        *int   `json:"retry_count"`
	RetryDelay        *int   `json:"retry_delay"`
	KeepAlive         *bool  `json:"keep_alive"`
	ReconnectInterval *int   `json:"reconnect_interval"`
}

type ModbusTCPConfig struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Timeout           int    `json:"timeout"`
	RetryCount        int    `json:"retry_count"`
	RetryDelay        int    `json:"retry_delay"`
	KeepAlive         bool   `json:"keep_alive"`
	ReconnectInterval int    `json:"reconnect_interval"`
}

type ModbusClientFactory func(
	config ModbusTCPConfig,
) (ModbusClient, error)

type modbusTCPDriver struct {
	clients ModbusClientFactory
}

func NewModbusTCPDriver(
	clients ModbusClientFactory,
) (GatewayDriver, error) {
	if clients == nil {
		return nil, ErrGatewayClientFactoryRequired
	}

	return &modbusTCPDriver{clients: clients}, nil
}

func NewDefaultModbusTCPDriver() GatewayDriver {
	return &modbusTCPDriver{clients: NewModbusTCPClient}
}

func (d *modbusTCPDriver) NormalizeConfig(
	raw json.RawMessage,
) (json.RawMessage, error) {
	var input ModbusTCPConfigInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf(
			"%w: decode Modbus TCP config: %v",
			ErrInvalidGatewayConfig,
			err,
		)
	}
	if err := ensureJSONDocumentEnded(decoder); err != nil {
		return nil, fmt.Errorf(
			"%w: decode Modbus TCP config: %v",
			ErrInvalidGatewayConfig,
			err,
		)
	}

	config := ModbusTCPConfig{
		Host: strings.TrimSpace(input.Host),
		Port: valueOrDefault(input.Port, DefaultModbusTCPPort),
		Timeout: valueOrDefault(
			input.Timeout,
			DefaultModbusTCPTimeout,
		),
		RetryCount: valueOrDefault(
			input.RetryCount,
			DefaultModbusTCPRetryCount,
		),
		RetryDelay: valueOrDefault(
			input.RetryDelay,
			DefaultModbusTCPRetryDelay,
		),
		KeepAlive: valueOrDefault(input.KeepAlive, true),
		ReconnectInterval: valueOrDefault(
			input.ReconnectInterval,
			DefaultModbusTCPReconnectInterval,
		),
	}

	if err := validateModbusTCPGatewayConfig(config); err != nil {
		return nil, err
	}

	canonical, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: encode Modbus TCP config: %v",
			ErrInvalidGatewayConfig,
			err,
		)
	}

	return canonical, nil
}

func (d *modbusTCPDriver) NewClient(
	raw json.RawMessage,
) (GatewayClient, error) {
	var config ModbusTCPConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf(
			"%w: decode stored Modbus TCP config: %v",
			ErrInvalidGatewayConfig,
			err,
		)
	}
	if err := validateModbusTCPGatewayConfig(config); err != nil {
		return nil, err
	}

	return d.clients(config)
}

func validateModbusTCPGatewayConfig(
	config ModbusTCPConfig,
) error {
	if config.Host == "" {
		return fmt.Errorf(
			"%w: host is required",
			ErrInvalidGatewayConfig,
		)
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf(
			"%w: port must be between 1 and 65535",
			ErrInvalidGatewayConfig,
		)
	}
	if config.Timeout < 100 || config.Timeout > 60000 {
		return fmt.Errorf(
			"%w: timeout must be between 100 and 60000 milliseconds",
			ErrInvalidGatewayConfig,
		)
	}
	if config.RetryCount < 0 || config.RetryCount > 10 {
		return fmt.Errorf(
			"%w: retry count must be between 0 and 10",
			ErrInvalidGatewayConfig,
		)
	}
	if config.RetryDelay < 0 {
		return fmt.Errorf(
			"%w: retry delay must not be negative",
			ErrInvalidGatewayConfig,
		)
	}
	if config.ReconnectInterval < 0 {
		return fmt.Errorf(
			"%w: reconnect interval must not be negative",
			ErrInvalidGatewayConfig,
		)
	}

	return nil
}

func ensureJSONDocumentEnded(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func valueOrDefault[T any](value *T, defaultValue T) T {
	if value == nil {
		return defaultValue
	}
	return *value
}

var _ GatewayDriver = (*modbusTCPDriver)(nil)
