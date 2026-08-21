package modbus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

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

type ModbusDeviceConfigInput struct {
	UnitID           *uint8 `json:"unit_id"`
	PollIntervalMS   *int   `json:"poll_interval_ms"`
	RequestTimeoutMS *int   `json:"request_timeout_ms"`
}

type ModbusDeviceConfig struct {
	UnitID           uint8 `json:"unit_id"`
	PollIntervalMS   int   `json:"poll_interval_ms"`
	RequestTimeoutMS *int  `json:"request_timeout_ms"`
}

type ModbusDatasourceConfigInput struct {
	FunctionCode   *FunctionCode `json:"function_code"`
	StartAddress   *uint16       `json:"start_address"`
	Quantity       *uint16       `json:"quantity"`
	PollIntervalMS *int          `json:"poll_interval_ms"`
}

type ModbusDatasourceConfig struct {
	FunctionCode   FunctionCode `json:"function_code"`
	StartAddress   uint16       `json:"start_address"`
	Quantity       uint16       `json:"quantity"`
	PollIntervalMS *int         `json:"poll_interval_ms"`
}

type ModbusClientFactory func(
	config ModbusTCPConfig,
) (ModbusClient, error)

type modbusTCPDriver struct {
	clients ModbusClientFactory
}

func NewModbusTCPDriver(
	clients ModbusClientFactory,
) (protocol.Driver, error) {
	if clients == nil {
		return nil, protocol.ErrGatewayClientFactoryRequired
	}

	return &modbusTCPDriver{clients: clients}, nil
}

func NewDefaultModbusTCPDriver() protocol.Driver {
	return &modbusTCPDriver{clients: NewModbusTCPClient}
}

func (d *modbusTCPDriver) GatewayType() string { return "modbus_tcp" }

func (d *modbusTCPDriver) DeviceType() string { return "modbus_device" }

func (d *modbusTCPDriver) DatasourceTypes() []string {
	return []string{"modbus_read"}
}

func (d *modbusTCPDriver) NormalizeDeviceConfig(raw json.RawMessage) (json.RawMessage, error) {
	var input ModbusDeviceConfigInput
	if err := decodeStrictJSON(raw, &input); err != nil {
		return nil, fmt.Errorf("%w: decode Modbus device config: %v", protocol.ErrInvalidDeviceConfig, err)
	}
	if input.UnitID == nil {
		return nil, fmt.Errorf("%w: unit_id is required", protocol.ErrInvalidDeviceConfig)
	}
	config := ModbusDeviceConfig{
		UnitID:           *input.UnitID,
		PollIntervalMS:   valueOrDefault(input.PollIntervalMS, 1000),
		RequestTimeoutMS: input.RequestTimeoutMS,
	}
	if config.PollIntervalMS < 100 || config.PollIntervalMS > 86400000 {
		return nil, fmt.Errorf("%w: poll_interval_ms must be between 100 and 86400000", protocol.ErrInvalidDeviceConfig)
	}
	if config.RequestTimeoutMS != nil && (*config.RequestTimeoutMS < 100 || *config.RequestTimeoutMS > 60000) {
		return nil, fmt.Errorf("%w: request_timeout_ms must be between 100 and 60000", protocol.ErrInvalidDeviceConfig)
	}
	return marshalCanonical(config, protocol.ErrInvalidDeviceConfig)
}

func (d *modbusTCPDriver) NormalizeDatasourceConfig(datasourceType string, raw json.RawMessage) (json.RawMessage, error) {
	if datasourceType != "modbus_read" {
		return nil, fmt.Errorf("%w: unsupported datasource type %q", protocol.ErrInvalidDatasourceConfig, datasourceType)
	}
	var input ModbusDatasourceConfigInput
	if err := decodeStrictJSON(raw, &input); err != nil {
		return nil, fmt.Errorf("%w: decode Modbus datasource config: %v", protocol.ErrInvalidDatasourceConfig, err)
	}
	if input.FunctionCode == nil || input.StartAddress == nil || input.Quantity == nil {
		return nil, fmt.Errorf("%w: function_code, start_address, and quantity are required", protocol.ErrInvalidDatasourceConfig)
	}
	config := ModbusDatasourceConfig{
		FunctionCode:   *input.FunctionCode,
		StartAddress:   *input.StartAddress,
		Quantity:       *input.Quantity,
		PollIntervalMS: input.PollIntervalMS,
	}
	if err := validateReadRequest(ReadRequest{FunctionCode: config.FunctionCode, Address: config.StartAddress, Quantity: config.Quantity}); err != nil {
		return nil, fmt.Errorf("%w: %v", protocol.ErrInvalidDatasourceConfig, err)
	}
	if config.PollIntervalMS != nil && (*config.PollIntervalMS < 100 || *config.PollIntervalMS > 86400000) {
		return nil, fmt.Errorf("%w: poll_interval_ms must be between 100 and 86400000", protocol.ErrInvalidDatasourceConfig)
	}
	return marshalCanonical(config, protocol.ErrInvalidDatasourceConfig)
}

func (d *modbusTCPDriver) Preview(ctx context.Context, request protocol.DatasourceReadRequest) (protocol.DatasourceSample, error) {
	client, readRequest, err := d.prepareDatasourceRead(request)
	if err != nil {
		return protocol.DatasourceSample{}, err
	}
	var sample protocol.DatasourceSample
	err = executeExclusive(ctx, request.ExecuteExclusive, func(exclusiveCtx context.Context) error {
		if err := client.Connect(exclusiveCtx); err != nil {
			return err
		}
		defer client.Disconnect()
		var readErr error
		sample, readErr = readModbusSample(exclusiveCtx, client, readRequest)
		return readErr
	})
	return sample, err
}

func (d *modbusTCPDriver) Monitor(ctx context.Context, request protocol.DatasourceReadRequest, interval time.Duration, emit protocol.SampleEmitter) error {
	client, readRequest, err := d.prepareDatasourceRead(request)
	if err != nil {
		return err
	}
	defer client.Disconnect()

	read := func() {
		var sample protocol.DatasourceSample
		err := executeExclusive(ctx, request.ExecuteExclusive, func(exclusiveCtx context.Context) error {
			if !client.IsConnected() {
				if err := client.Connect(exclusiveCtx); err != nil {
					return err
				}
			}
			var readErr error
			sample, readErr = readModbusSample(exclusiveCtx, client, readRequest)
			if readErr != nil {
				_ = client.Disconnect()
			}
			return readErr
		})
		if err != nil {
			emit(protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "bad", Error: "Read failed"})
			return
		}
		emit(sample)
	}

	read()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			read()
		}
	}
}

func executeExclusive(ctx context.Context, execute protocol.ExclusiveExecutor, operation func(context.Context) error) error {
	if execute == nil {
		return operation(ctx)
	}
	return execute(ctx, operation)
}

func (d *modbusTCPDriver) prepareDatasourceRead(request protocol.DatasourceReadRequest) (ModbusClient, ReadRequest, error) {
	var gateway ModbusTCPConfig
	var device ModbusDeviceConfig
	var datasource ModbusDatasourceConfig
	if err := json.Unmarshal(request.GatewayConfig, &gateway); err != nil {
		return nil, ReadRequest{}, fmt.Errorf("decode stored Modbus gateway config: %w", err)
	}
	if err := json.Unmarshal(request.DeviceConfig, &device); err != nil {
		return nil, ReadRequest{}, fmt.Errorf("decode stored Modbus device config: %w", err)
	}
	if err := json.Unmarshal(request.DatasourceConfig, &datasource); err != nil {
		return nil, ReadRequest{}, fmt.Errorf("decode stored Modbus datasource config: %w", err)
	}
	if device.RequestTimeoutMS != nil {
		gateway.Timeout = *device.RequestTimeoutMS
	}
	client, err := d.clients(gateway)
	if err != nil {
		return nil, ReadRequest{}, err
	}
	return client, ReadRequest{UnitID: device.UnitID, FunctionCode: datasource.FunctionCode, Address: datasource.StartAddress, Quantity: datasource.Quantity}, nil
}

func readModbusSample(ctx context.Context, client ModbusClient, request ReadRequest) (protocol.DatasourceSample, error) {
	started := time.Now()
	raw, err := client.Read(ctx, request)
	if err != nil {
		return protocol.DatasourceSample{}, err
	}
	data, err := formatModbusData(request, raw)
	if err != nil {
		return protocol.DatasourceSample{}, err
	}
	return protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Latency: time.Since(started), Quality: "good", Raw: raw, Data: data}, nil
}

func formatModbusData(request ReadRequest, raw []byte) (json.RawMessage, error) {
	if request.FunctionCode == FunctionReadCoils || request.FunctionCode == FunctionReadDiscreteInputs {
		requiredBytes := (int(request.Quantity) + 7) / 8
		if len(raw) < requiredBytes {
			return nil, errors.New("short Modbus bit response")
		}
		bits := make([]map[string]any, 0, request.Quantity)
		for offset := uint16(0); offset < request.Quantity; offset++ {
			bits = append(bits, map[string]any{"address": uint32(request.Address) + uint32(offset), "value": raw[offset/8]&(1<<(offset%8)) != 0})
		}
		return json.Marshal(map[string]any{"bits": bits})
	}
	registers := make([]map[string]any, 0, request.Quantity)
	for offset := uint16(0); offset < request.Quantity; offset++ {
		index := int(offset) * 2
		if index+1 >= len(raw) {
			return nil, errors.New("short Modbus register response")
		}
		value := uint16(raw[index])<<8 | uint16(raw[index+1])
		registers = append(registers, map[string]any{"address": uint32(request.Address) + uint32(offset), "value": value, "hex": fmt.Sprintf("%04X", value)})
	}
	return json.Marshal(map[string]any{"registers": registers})
}

func decodeStrictJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONDocumentEnded(decoder)
}

func marshalCanonical(value any, sentinel error) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical config: %v", sentinel, err)
	}
	return encoded, nil
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

var _ protocol.Driver = (*modbusTCPDriver)(nil)
