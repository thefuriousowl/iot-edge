package modbus

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func TestNewModbusTCPDriverRequiresFactory(t *testing.T) {
	t.Parallel()

	driver, err := NewModbusTCPDriver(nil)
	if driver != nil {
		t.Fatalf("NewModbusTCPDriver() driver = %#v, want nil", driver)
	}
	if !errors.Is(err, protocol.ErrGatewayClientFactoryRequired) {
		t.Fatalf("NewModbusTCPDriver() error = %v, want factory error", err)
	}
}

func TestModbusTCPDriverNormalizeConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	driver := newTestModbusTCPDriver(t, nil)
	got := normalizeAndDecodeModbusConfig(t, driver, ModbusTCPConfigInput{
		Host: "  plc.example.local  ",
	})
	want := ModbusTCPConfig{
		Host:              "plc.example.local",
		Port:              DefaultModbusTCPPort,
		Timeout:           DefaultModbusTCPTimeout,
		RetryCount:        DefaultModbusTCPRetryCount,
		RetryDelay:        DefaultModbusTCPRetryDelay,
		KeepAlive:         true,
		ReconnectInterval: DefaultModbusTCPReconnectInterval,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeConfig() = %#v, want %#v", got, want)
	}
}

func TestModbusTCPDriverNormalizeConfigPreservesExplicitValues(t *testing.T) {
	t.Parallel()

	port := 1502
	timeout := 100
	retryCount := 0
	retryDelay := 0
	keepAlive := false
	reconnectInterval := 0
	driver := newTestModbusTCPDriver(t, nil)
	got := normalizeAndDecodeModbusConfig(t, driver, ModbusTCPConfigInput{
		Host:              "192.0.2.25",
		Port:              &port,
		Timeout:           &timeout,
		RetryCount:        &retryCount,
		RetryDelay:        &retryDelay,
		KeepAlive:         &keepAlive,
		ReconnectInterval: &reconnectInterval,
	})
	want := ModbusTCPConfig{
		Host:              "192.0.2.25",
		Port:              1502,
		Timeout:           100,
		RetryCount:        0,
		RetryDelay:        0,
		KeepAlive:         false,
		ReconnectInterval: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeConfig() = %#v, want %#v", got, want)
	}
}

func TestModbusTCPDriverNormalizeConfigAcceptsBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		port       int
		timeout    int
		retryCount int
	}{
		{name: "minimum values", port: 1, timeout: 100, retryCount: 0},
		{name: "maximum values", port: 65535, timeout: 60000, retryCount: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			driver := newTestModbusTCPDriver(t, nil)
			normalizeAndDecodeModbusConfig(t, driver, ModbusTCPConfigInput{
				Host:       "plc.example.local",
				Port:       &tt.port,
				Timeout:    &tt.timeout,
				RetryCount: &tt.retryCount,
			})
		})
	}
}

func TestModbusTCPDriverNormalizeConfigRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      ModbusTCPConfigInput
		wantDetail string
	}{
		{name: "empty host", input: ModbusTCPConfigInput{}, wantDetail: "host is required"},
		{name: "whitespace host", input: ModbusTCPConfigInput{Host: "  \t"}, wantDetail: "host is required"},
		{name: "port below minimum", input: ModbusTCPConfigInput{Host: "plc.example.local", Port: intPointer(0)}, wantDetail: "port must be between 1 and 65535"},
		{name: "port above maximum", input: ModbusTCPConfigInput{Host: "plc.example.local", Port: intPointer(65536)}, wantDetail: "port must be between 1 and 65535"},
		{name: "timeout below minimum", input: ModbusTCPConfigInput{Host: "plc.example.local", Timeout: intPointer(99)}, wantDetail: "timeout must be between 100 and 60000 milliseconds"},
		{name: "timeout above maximum", input: ModbusTCPConfigInput{Host: "plc.example.local", Timeout: intPointer(60001)}, wantDetail: "timeout must be between 100 and 60000 milliseconds"},
		{name: "retry count below minimum", input: ModbusTCPConfigInput{Host: "plc.example.local", RetryCount: intPointer(-1)}, wantDetail: "retry count must be between 0 and 10"},
		{name: "retry count above maximum", input: ModbusTCPConfigInput{Host: "plc.example.local", RetryCount: intPointer(11)}, wantDetail: "retry count must be between 0 and 10"},
		{name: "negative retry delay", input: ModbusTCPConfigInput{Host: "plc.example.local", RetryDelay: intPointer(-1)}, wantDetail: "retry delay must not be negative"},
		{name: "negative reconnect interval", input: ModbusTCPConfigInput{Host: "plc.example.local", ReconnectInterval: intPointer(-1)}, wantDetail: "reconnect interval must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			driver := newTestModbusTCPDriver(t, nil)
			raw, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			got, err := driver.NormalizeConfig(raw)
			if !errors.Is(err, protocol.ErrInvalidGatewayConfig) {
				t.Fatalf("NormalizeConfig() error = %v, want config error", err)
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("error = %q, want detail %q", err, tt.wantDetail)
			}
			if got != nil {
				t.Fatalf("invalid normalized config = %s, want nil", got)
			}
		})
	}
}

func TestModbusTCPDriverNormalizeConfigRejectsInvalidJSONDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty document", raw: ""},
		{name: "malformed document", raw: `{"host":`},
		{name: "unknown field", raw: `{"host":"plc.local","tls":true}`},
		{name: "multiple values", raw: `{"host":"plc.local"} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			driver := newTestModbusTCPDriver(t, nil)
			config, err := driver.NormalizeConfig(json.RawMessage(tt.raw))
			if config != nil {
				t.Fatalf("NormalizeConfig() = %s, want nil", config)
			}
			if !errors.Is(err, protocol.ErrInvalidGatewayConfig) {
				t.Fatalf("NormalizeConfig() error = %v, want config error", err)
			}
		})
	}
}

func TestModbusTCPDriverNewClientDecodesTypedConfigLazily(t *testing.T) {
	t.Parallel()

	factoryCalls := 0
	var gotConfig ModbusTCPConfig
	driver := newTestModbusTCPDriver(t, func(config ModbusTCPConfig) (ModbusClient, error) {
		factoryCalls++
		gotConfig = config
		return nil, nil
	})
	raw := mustMarshalConfig(t, ModbusTCPConfig{
		Host:              "plc.local",
		Port:              502,
		Timeout:           5000,
		RetryCount:        3,
		RetryDelay:        1000,
		KeepAlive:         true,
		ReconnectInterval: 30,
	})

	client, err := driver.NewClient(raw)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client != nil {
		t.Fatalf("NewClient() client = %#v, want factory result nil", client)
	}
	if factoryCalls != 1 || gotConfig.Host != "plc.local" {
		t.Fatalf("factory calls/config = (%d, %#v), want one typed call", factoryCalls, gotConfig)
	}
}

func TestModbusTCPDriverPreparesConnectionOnlyTestWithoutUnitID(t *testing.T) {
	t.Parallel()

	driver := newTestModbusTCPDriver(t, nil)
	tests := []struct {
		name    string
		options json.RawMessage
	}{
		{name: "omitted options", options: nil},
		{name: "empty object", options: json.RawMessage(`{}`)},
		{name: "null options", options: json.RawMessage(`null`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			probe, err := driver.PrepareConnectionTest(tt.options)
			if err != nil {
				t.Fatalf("PrepareConnectionTest() error = %v", err)
			}
			if probe == nil {
				t.Fatal("PrepareConnectionTest() probe = nil")
			}
			if err := probe(context.Background(), &gatewayOnlyClient{}); err != nil {
				t.Fatalf("connection-only probe error = %v", err)
			}
		})
	}
}

func TestModbusTCPDriverConnectionProbeReadsHoldingRegister(t *testing.T) {
	t.Parallel()

	driver := newTestModbusTCPDriver(t, nil)
	probe, err := driver.PrepareConnectionTest(
		json.RawMessage(`{"unit_id":7}`),
	)
	if err != nil {
		t.Fatalf("PrepareConnectionTest() error = %v", err)
	}
	client := &fakeModbusClient{readData: []byte{0x12, 0x34}}
	if err := probe(context.Background(), client); err != nil {
		t.Fatalf("probe() error = %v", err)
	}
	want := ReadRequest{
		UnitID:       7,
		FunctionCode: FunctionReadHoldingRegisters,
		Address:      0,
		Quantity:     1,
	}
	if client.lastRequest != want {
		t.Fatalf("probe request = %#v, want %#v", client.lastRequest, want)
	}
}

func TestModbusTCPDriverConnectionTestRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options string
	}{
		{name: "malformed", options: `{"unit_id":`},
		{name: "unknown field", options: `{"unit_id":1,"address":0}`},
		{name: "negative unit ID", options: `{"unit_id":-1}`},
		{name: "unit ID above maximum", options: `{"unit_id":256}`},
		{name: "fractional unit ID", options: `{"unit_id":1.5}`},
		{name: "multiple documents", options: `{"unit_id":1} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			driver := newTestModbusTCPDriver(t, nil)
			probe, err := driver.PrepareConnectionTest(
				json.RawMessage(tt.options),
			)
			if probe != nil {
				t.Fatalf("PrepareConnectionTest() probe = %#v, want nil", probe)
			}
			if !errors.Is(err, protocol.ErrInvalidGatewayTestOptions) {
				t.Fatalf("PrepareConnectionTest() error = %v, want invalid options", err)
			}
		})
	}
}

func TestModbusTCPDriverConnectionProbePreservesReadAndClientErrors(t *testing.T) {
	t.Parallel()

	driver := newTestModbusTCPDriver(t, nil)
	probe, err := driver.PrepareConnectionTest(
		json.RawMessage(`{"unit_id":1}`),
	)
	if err != nil {
		t.Fatalf("PrepareConnectionTest() error = %v", err)
	}

	readErr := errors.New("Modbus exception")
	if err := probe(context.Background(), &fakeModbusClient{readErr: readErr}); !errors.Is(err, readErr) {
		t.Fatalf("probe() read error = %v, want %v", err, readErr)
	}
	if err := probe(context.Background(), &gatewayOnlyClient{}); !errors.Is(err, protocol.ErrGatewayClientType) {
		t.Fatalf("probe() client error = %v, want client type error", err)
	}
}

type gatewayOnlyClient struct {
	connected bool
}

func (c *gatewayOnlyClient) Connect(context.Context) error {
	c.connected = true
	return nil
}

func (c *gatewayOnlyClient) Disconnect() error {
	c.connected = false
	return nil
}

func (c *gatewayOnlyClient) IsConnected() bool {
	return c.connected
}

var _ protocol.GatewayClient = (*gatewayOnlyClient)(nil)

func newTestModbusTCPDriver(
	t *testing.T,
	factory ModbusClientFactory,
) protocol.GatewayDriver {
	t.Helper()
	if factory == nil {
		factory = func(ModbusTCPConfig) (ModbusClient, error) {
			return nil, nil
		}
	}
	driver, err := NewModbusTCPDriver(factory)
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	return driver
}

func normalizeAndDecodeModbusConfig(
	t *testing.T,
	driver protocol.GatewayDriver,
	input ModbusTCPConfigInput,
) ModbusTCPConfig {
	t.Helper()
	raw := mustMarshalConfig(t, input)
	normalized, err := driver.NormalizeConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	var config ModbusTCPConfig
	if err := json.Unmarshal(normalized, &config); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return config
}

func mustMarshalConfig(t *testing.T, config any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func intPointer(value int) *int {
	return &value
}
