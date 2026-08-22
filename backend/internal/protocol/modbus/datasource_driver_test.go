package modbus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aldas/go-modbus-client/packet"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func TestDatasourceDriverNormalizesProtocolOwnedConfigs(t *testing.T) {
	t.Parallel()
	driver := newTestModbusTCPDriver(t, nil)

	deviceConfig, err := driver.NormalizeDeviceConfig(json.RawMessage(`{"unit_id":7}`))
	if err != nil {
		t.Fatalf("NormalizeDeviceConfig() error = %v", err)
	}
	if string(deviceConfig) != `{"unit_id":7,"poll_interval_ms":1000,"request_timeout_ms":null}` {
		t.Errorf("device config = %s", deviceConfig)
	}

	datasourceConfig, err := driver.NormalizeDatasourceConfig("modbus_read", json.RawMessage(`{"function_code":3,"start_address":10,"quantity":2}`))
	if err != nil {
		t.Fatalf("NormalizeDatasourceConfig() error = %v", err)
	}
	if string(datasourceConfig) != `{"function_code":3,"start_address":10,"quantity":2,"poll_interval_ms":null}` {
		t.Errorf("datasource config = %s", datasourceConfig)
	}
}

func TestDatasourceDriverRejectsInvalidAndUnknownConfigs(t *testing.T) {
	t.Parallel()
	driver := newTestModbusTCPDriver(t, nil)
	tests := []struct {
		name     string
		run      func() error
		sentinel error
	}{
		{name: "missing unit", run: func() error { _, err := driver.NormalizeDeviceConfig(json.RawMessage(`{}`)); return err }, sentinel: protocol.ErrInvalidDeviceConfig},
		{name: "short interval", run: func() error {
			_, err := driver.NormalizeDeviceConfig(json.RawMessage(`{"unit_id":1,"poll_interval_ms":99}`))
			return err
		}, sentinel: protocol.ErrInvalidDeviceConfig},
		{name: "unknown datasource type", run: func() error {
			_, err := driver.NormalizeDatasourceConfig("mqtt_subscription", json.RawMessage(`{}`))
			return err
		}, sentinel: protocol.ErrInvalidDatasourceConfig},
		{name: "quantity over protocol maximum", run: func() error {
			_, err := driver.NormalizeDatasourceConfig("modbus_read", json.RawMessage(`{"function_code":3,"start_address":0,"quantity":126}`))
			return err
		}, sentinel: protocol.ErrInvalidDatasourceConfig},
		{name: "unknown field", run: func() error {
			_, err := driver.NormalizeDatasourceConfig("modbus_read", json.RawMessage(`{"function_code":3,"start_address":0,"quantity":1,"topic":"x"}`))
			return err
		}, sentinel: protocol.ErrInvalidDatasourceConfig},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.sentinel) {
				t.Fatalf("error = %v, want %v", err, test.sentinel)
			}
		})
	}
}

func TestDatasourceDriverPreviewReturnsRegisterEnvelope(t *testing.T) {
	t.Parallel()
	client := &fakeModbusClient{readData: []byte{0x12, 0x34, 0xAB, 0xCD}}
	driver, err := NewModbusTCPDriver(func(ModbusTCPConfig) (ModbusClient, error) { return client, nil })
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	exclusiveCalls := 0
	sample, err := driver.Preview(context.Background(), protocol.DatasourceReadRequest{
		GatewayConfig:    json.RawMessage(`{"host":"plc.local","port":502,"timeout":5000,"retry_count":0,"retry_delay":0,"keep_alive":true,"reconnect_interval":0}`),
		DeviceConfig:     json.RawMessage(`{"unit_id":7,"poll_interval_ms":1000,"request_timeout_ms":null}`),
		DatasourceConfig: json.RawMessage(`{"function_code":3,"start_address":10,"quantity":2,"poll_interval_ms":null}`),
		ExecuteExclusive: func(ctx context.Context, operation func(context.Context) error) error {
			exclusiveCalls++
			return operation(ctx)
		},
	})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if sample.Quality != "good" || string(sample.Raw) != string([]byte{0x12, 0x34, 0xAB, 0xCD}) {
		t.Errorf("sample = %#v", sample)
	}
	if string(sample.Data) != `{"registers":[{"address":10,"hex":"1234","value":4660},{"address":11,"hex":"ABCD","value":43981}]}` {
		t.Errorf("data = %s", sample.Data)
	}
	if client.lastRequest.UnitID != 7 || client.lastRequest.Address != 10 {
		t.Errorf("request = %#v", client.lastRequest)
	}
	if exclusiveCalls != 1 {
		t.Errorf("exclusive executor calls = %d, want 1", exclusiveCalls)
	}
}

func TestDatasourceDriverPreviewPreservesPublicModbusException(t *testing.T) {
	t.Parallel()
	exception := &packet.ErrorResponseTCP{UnitID: 7, Function: 3, Code: packet.ErrIllegalDataAddress}
	client := &fakeModbusClient{readErr: exception}
	driver, err := NewModbusTCPDriver(func(ModbusTCPConfig) (ModbusClient, error) { return client, nil })
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	_, err = driver.Preview(context.Background(), datasourceReadRequest())
	if !errors.Is(err, exception) {
		t.Fatalf("Preview() error = %v, want exception cause", err)
	}
	message, details, ok := protocol.PublicRequestError(err)
	if !ok || !strings.Contains(message, "Illegal Data Address") || details["exception_hex"] != "0x02" {
		t.Fatalf("public error = %q / %#v", message, details)
	}
}

func TestDatasourceDriverMonitorEmitsPublicModbusExceptionWithoutDroppingConnection(t *testing.T) {
	t.Parallel()
	exception := &packet.ErrorResponseTCP{UnitID: 7, Function: 3, Code: packet.ErrIllegalDataAddress}
	client := &fakeModbusClient{readErr: exception}
	driver, err := NewModbusTCPDriver(func(ModbusTCPConfig) (ModbusClient, error) { return client, nil })
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	connectedDuringEmit := false
	var emitted protocol.DatasourceSample
	err = driver.Monitor(ctx, datasourceReadRequest(), 100, func(sample protocol.DatasourceSample) {
		emitted = sample
		connectedDuringEmit = client.IsConnected()
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Monitor() error = %v, want context cancellation", err)
	}
	if emitted.Quality != "bad" || !strings.Contains(emitted.Error, "Modbus exception 0x02: Illegal Data Address") {
		t.Errorf("emitted sample = %#v", emitted)
	}
	if !connectedDuringEmit {
		t.Error("Monitor() disconnected a healthy Modbus transport after an exception response")
	}
}

func TestFormatModbusDataRejectsShortPayload(t *testing.T) {
	t.Parallel()
	if _, err := formatModbusData(ReadRequest{FunctionCode: FunctionReadCoils, Quantity: 9}, []byte{1}); err == nil {
		t.Error("bit payload error = nil")
	}
	if _, err := formatModbusData(ReadRequest{FunctionCode: FunctionReadHoldingRegisters, Quantity: 1}, []byte{1}); err == nil {
		t.Error("register payload error = nil")
	}
}

func datasourceReadRequest() protocol.DatasourceReadRequest {
	return protocol.DatasourceReadRequest{
		GatewayConfig:    json.RawMessage(`{"host":"plc.local","port":502,"timeout":5000,"retry_count":0,"retry_delay":0,"keep_alive":true,"reconnect_interval":0}`),
		DeviceConfig:     json.RawMessage(`{"unit_id":7,"poll_interval_ms":1000,"request_timeout_ms":null}`),
		DatasourceConfig: json.RawMessage(`{"function_code":3,"start_address":96,"quantity":5,"poll_interval_ms":null}`),
	}
}
