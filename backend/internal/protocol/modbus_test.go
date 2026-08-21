package protocol

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFunctionCodeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code FunctionCode
		want uint8
	}{
		{name: "read coils", code: FunctionReadCoils, want: 1},
		{name: "read discrete inputs", code: FunctionReadDiscreteInputs, want: 2},
		{name: "read holding registers", code: FunctionReadHoldingRegisters, want: 3},
		{name: "read input registers", code: FunctionReadInputRegisters, want: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := uint8(tt.code); got != tt.want {
				t.Fatalf("function code = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReadRequestPreservesProtocolBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request ReadRequest
	}{
		{
			name: "zero values remain representable for validation by adapter",
			request: ReadRequest{
				UnitID:       0,
				FunctionCode: FunctionReadCoils,
				Address:      0,
				Quantity:     0,
			},
		},
		{
			name: "maximum wire values are preserved",
			request: ReadRequest{
				UnitID:       255,
				FunctionCode: FunctionReadInputRegisters,
				Address:      65535,
				Quantity:     65535,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ReadRequest{
				UnitID:       tt.request.UnitID,
				FunctionCode: tt.request.FunctionCode,
				Address:      tt.request.Address,
				Quantity:     tt.request.Quantity,
			}
			if !reflect.DeepEqual(got, tt.request) {
				t.Fatalf("request = %#v, want %#v", got, tt.request)
			}
		})
	}
}

func TestModbusClientContract(t *testing.T) {
	t.Parallel()

	readErr := errors.New("read failed")
	client := &fakeModbusClient{
		connected: true,
		readData:  []byte{0x12, 0x34},
		readErr:   readErr,
	}
	request := ReadRequest{
		UnitID:       7,
		FunctionCode: FunctionReadHoldingRegisters,
		Address:      100,
		Quantity:     2,
	}

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if !client.IsConnected() {
		t.Fatal("IsConnected() = false, want true")
	}

	data, err := client.Read(context.Background(), request)
	if !errors.Is(err, readErr) {
		t.Fatalf("Read() error = %v, want %v", err, readErr)
	}
	if !reflect.DeepEqual(data, []byte{0x12, 0x34}) {
		t.Fatalf("Read() data = %v, want [18 52]", data)
	}
	if client.lastRequest != request {
		t.Fatalf("Read() request = %#v, want %#v", client.lastRequest, request)
	}

	if err := client.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if client.IsConnected() {
		t.Fatal("IsConnected() = true after Disconnect(), want false")
	}
}

type fakeModbusClient struct {
	connected   bool
	readData    []byte
	readErr     error
	lastRequest ReadRequest
}

var _ ModbusClient = (*fakeModbusClient)(nil)

func (c *fakeModbusClient) Connect(context.Context) error {
	c.connected = true
	return nil
}

func (c *fakeModbusClient) Disconnect() error {
	c.connected = false
	return nil
}

func (c *fakeModbusClient) IsConnected() bool {
	return c.connected
}

func (c *fakeModbusClient) Read(_ context.Context, request ReadRequest) ([]byte, error) {
	c.lastRequest = request
	return c.readData, c.readErr
}
