package modbus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/thefuriousowl/iot-edge/internal/protocol"
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

type ModbusTCPConnectionTestInput struct {
	UnitID *uint8 `json:"unit_id"`
}

type ModbusClientFactory func(
	config ModbusTCPConfig,
) (ModbusClient, error)

type modbusTCPDriver struct {
	clients ModbusClientFactory
}

func NewModbusTCPDriver(
	clients ModbusClientFactory,
) (protocol.GatewayDriver, error) {
	if clients == nil {
		return nil, protocol.ErrGatewayClientFactoryRequired
	}

	return &modbusTCPDriver{clients: clients}, nil
}

func NewDefaultModbusTCPDriver() protocol.GatewayDriver {
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
			protocol.ErrInvalidGatewayConfig,
			err,
		)
	}
	if err := ensureJSONDocumentEnded(decoder); err != nil {
		return nil, fmt.Errorf(
			"%w: decode Modbus TCP config: %v",
			protocol.ErrInvalidGatewayConfig,
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
			protocol.ErrInvalidGatewayConfig,
			err,
		)
	}

	return canonical, nil
}

func (d *modbusTCPDriver) NewClient(
	raw json.RawMessage,
) (protocol.GatewayClient, error) {
	var config ModbusTCPConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf(
			"%w: decode stored Modbus TCP config: %v",
			protocol.ErrInvalidGatewayConfig,
			err,
		)
	}
	if err := validateModbusTCPGatewayConfig(config); err != nil {
		return nil, err
	}

	return d.clients(config)
}

func (d *modbusTCPDriver) PrepareConnectionTest(
	raw json.RawMessage,
) (protocol.ConnectionProbe, error) {
	var input ModbusTCPConnectionTestInput
	if len(bytes.TrimSpace(raw)) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf(
				"%w: decode Modbus TCP test options: %v",
				protocol.ErrInvalidGatewayTestOptions,
				err,
			)
		}
		if err := ensureJSONDocumentEnded(decoder); err != nil {
			return nil, fmt.Errorf(
				"%w: decode Modbus TCP test options: %v",
				protocol.ErrInvalidGatewayTestOptions,
				err,
			)
		}
	}

	return func(
		ctx context.Context,
		client protocol.GatewayClient,
	) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if input.UnitID == nil {
			return nil
		}

		modbusClient, ok := client.(ModbusClient)
		if !ok {
			return fmt.Errorf(
				"%w: Modbus TCP driver received %T",
				protocol.ErrGatewayClientType,
				client,
			)
		}
		_, err := modbusClient.Read(ctx, ReadRequest{
			UnitID:       *input.UnitID,
			FunctionCode: FunctionReadHoldingRegisters,
			Address:      0,
			Quantity:     1,
		})
		if err != nil {
			return fmt.Errorf(
				"test Modbus TCP read for unit %d: %w",
				*input.UnitID,
				err,
			)
		}
		return nil
	}, nil
}

func validateModbusTCPGatewayConfig(
	config ModbusTCPConfig,
) error {
	if config.Host == "" {
		return fmt.Errorf(
			"%w: host is required",
			protocol.ErrInvalidGatewayConfig,
		)
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf(
			"%w: port must be between 1 and 65535",
			protocol.ErrInvalidGatewayConfig,
		)
	}
	if config.Timeout < 100 || config.Timeout > 60000 {
		return fmt.Errorf(
			"%w: timeout must be between 100 and 60000 milliseconds",
			protocol.ErrInvalidGatewayConfig,
		)
	}
	if config.RetryCount < 0 || config.RetryCount > 10 {
		return fmt.Errorf(
			"%w: retry count must be between 0 and 10",
			protocol.ErrInvalidGatewayConfig,
		)
	}
	if config.RetryDelay < 0 {
		return fmt.Errorf(
			"%w: retry delay must not be negative",
			protocol.ErrInvalidGatewayConfig,
		)
	}
	if config.ReconnectInterval < 0 {
		return fmt.Errorf(
			"%w: reconnect interval must not be negative",
			protocol.ErrInvalidGatewayConfig,
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

var _ protocol.GatewayDriver = (*modbusTCPDriver)(nil)
