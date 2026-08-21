package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aldas/go-modbus-client/packet"
)

func TestValidateReadRequest(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name    string
		request ReadRequest
	}{
		{
			name: "FC01 maximum quantity ending at maximum address",
			request: ReadRequest{
				FunctionCode: FunctionReadCoils,
				Address:      63536,
				Quantity:     2000,
			},
		},
		{
			name: "FC02 maximum quantity",
			request: ReadRequest{
				FunctionCode: FunctionReadDiscreteInputs,
				Quantity:     2000,
			},
		},
		{
			name: "FC03 maximum quantity",
			request: ReadRequest{
				FunctionCode: FunctionReadHoldingRegisters,
				Quantity:     125,
			},
		},
		{
			name: "FC04 maximum quantity ending at maximum address",
			request: ReadRequest{
				FunctionCode: FunctionReadInputRegisters,
				Address:      65411,
				Quantity:     125,
			},
		},
	}

	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := validateReadRequest(tt.request); err != nil {
				t.Fatalf("validateReadRequest() error = %v", err)
			}
		})
	}

	invalid := []struct {
		name       string
		request    ReadRequest
		wantDetail string
	}{
		{
			name: "zero quantity",
			request: ReadRequest{
				FunctionCode: FunctionReadCoils,
			},
			wantDetail: "quantity must be greater than zero",
		},
		{
			name: "unsupported function code",
			request: ReadRequest{
				FunctionCode: 5,
				Quantity:     1,
			},
			wantDetail: "unsupported function code 5",
		},
		{
			name: "FC01 quantity above maximum",
			request: ReadRequest{
				FunctionCode: FunctionReadCoils,
				Quantity:     2001,
			},
			wantDetail: "exceeds maximum 2000",
		},
		{
			name: "FC02 quantity above maximum",
			request: ReadRequest{
				FunctionCode: FunctionReadDiscreteInputs,
				Quantity:     2001,
			},
			wantDetail: "exceeds maximum 2000",
		},
		{
			name: "FC03 quantity above maximum",
			request: ReadRequest{
				FunctionCode: FunctionReadHoldingRegisters,
				Quantity:     126,
			},
			wantDetail: "exceeds maximum 125",
		},
		{
			name: "FC04 quantity above maximum",
			request: ReadRequest{
				FunctionCode: FunctionReadInputRegisters,
				Quantity:     126,
			},
			wantDetail: "exceeds maximum 125",
		},
		{
			name: "address range overflow",
			request: ReadRequest{
				FunctionCode: FunctionReadHoldingRegisters,
				Address:      65535,
				Quantity:     2,
			},
			wantDetail: "exceeds maximum address 65535",
		},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateReadRequest(tt.request)
			if !errors.Is(err, ErrInvalidModbusRequest) {
				t.Fatalf("error = %v, want ErrInvalidModbusRequest", err)
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("error = %q, want detail %q", err, tt.wantDetail)
			}
		})
	}
}

func TestBuildReadPacketRejectsUnsupportedFunction(t *testing.T) {
	t.Parallel()

	request, err := buildReadPacket(ReadRequest{
		FunctionCode: 99,
		Quantity:     1,
	})
	if request != nil {
		t.Fatalf("buildReadPacket() = %#v, want nil", request)
	}
	if !errors.Is(err, ErrInvalidModbusRequest) {
		t.Fatalf("error = %v, want ErrInvalidModbusRequest", err)
	}
}

func TestModbusTCPClientReadFunctionCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		functionCode FunctionCode
		response     packet.Response
		wantType     any
	}{
		{
			name:         "FC01 read coils",
			functionCode: FunctionReadCoils,
			response: &packet.ReadCoilsResponseTCP{
				ReadCoilsResponse: packet.ReadCoilsResponse{Data: []byte{0b00000101}},
			},
			wantType: (*packet.ReadCoilsRequestTCP)(nil),
		},
		{
			name:         "FC02 read discrete inputs",
			functionCode: FunctionReadDiscreteInputs,
			response: &packet.ReadDiscreteInputsResponseTCP{
				ReadDiscreteInputsResponse: packet.ReadDiscreteInputsResponse{Data: []byte{0b00001010}},
			},
			wantType: (*packet.ReadDiscreteInputsRequestTCP)(nil),
		},
		{
			name:         "FC03 read holding registers",
			functionCode: FunctionReadHoldingRegisters,
			response: &packet.ReadHoldingRegistersResponseTCP{
				ReadHoldingRegistersResponse: packet.ReadHoldingRegistersResponse{Data: []byte{0x12, 0x34}},
			},
			wantType: (*packet.ReadHoldingRegistersRequestTCP)(nil),
		},
		{
			name:         "FC04 read input registers",
			functionCode: FunctionReadInputRegisters,
			response: &packet.ReadInputRegistersResponseTCP{
				ReadInputRegistersResponse: packet.ReadInputRegistersResponse{Data: []byte{0xAB, 0xCD}},
			},
			wantType: (*packet.ReadInputRegistersRequestTCP)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotRequest packet.Request
			transport := &fakeModbusTransport{
				doFunc: func(_ context.Context, request packet.Request) (packet.Response, error) {
					gotRequest = request
					return tt.response, nil
				},
			}
			client := newConnectedTestModbusTCPClient(transport)
			request := ReadRequest{
				UnitID:       17,
				FunctionCode: tt.functionCode,
				Address:      321,
				Quantity:     1,
			}

			got, err := client.Read(context.Background(), request)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			wantData := readResponseData(t, tt.response)
			if fmt.Sprint(got) != fmt.Sprint(wantData) {
				t.Fatalf("Read() data = %v, want %v", got, wantData)
			}
			assertReadPacket(t, gotRequest, tt.wantType, request)
		})
	}
}

func TestModbusTCPClientReadThroughInjectedTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		functionCode FunctionCode
		responseData []byte
	}{
		{name: "FC01", functionCode: FunctionReadCoils, responseData: []byte{0b00000101}},
		{name: "FC02", functionCode: FunctionReadDiscreteInputs, responseData: []byte{0b00001010}},
		{name: "FC03", functionCode: FunctionReadHoldingRegisters, responseData: []byte{0x12, 0x34}},
		{name: "FC04", functionCode: FunctionReadInputRegisters, responseData: []byte{0xAB, 0xCD}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				_ = clientConn.Close()
				_ = serverConn.Close()
			})

			serverErr := make(chan error, 1)
			go serveSingleModbusRead(serverConn, ReadRequest{
				UnitID:       17,
				FunctionCode: tt.functionCode,
				Address:      321,
				Quantity:     1,
			}, tt.responseData, serverErr)

			config := validModbusTCPConfig()
			config.Timeout = 500
			client, err := newModbusTCPClient(
				config,
				func(context.Context, string) (net.Conn, error) {
					return clientConn, nil
				},
			)
			if err != nil {
				t.Fatalf("newModbusTCPClient() error = %v", err)
			}
			if err := client.Connect(context.Background()); err != nil {
				t.Fatalf("Connect() error = %v", err)
			}

			got, err := client.Read(context.Background(), ReadRequest{
				UnitID:       17,
				FunctionCode: tt.functionCode,
				Address:      321,
				Quantity:     1,
			})
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.responseData) {
				t.Fatalf("Read() data = %v, want %v", got, tt.responseData)
			}

			select {
			case err := <-serverErr:
				if err != nil {
					t.Fatalf("simulated Modbus server error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for simulated Modbus server")
			}

			if err := client.Disconnect(); err != nil {
				t.Fatalf("Disconnect() error = %v", err)
			}
		})
	}
}

func TestModbusTCPClientPreservesModbusException(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	serverErr := make(chan error, 1)
	go func() {
		request := make([]byte, 12)
		if _, err := io.ReadFull(serverConn, request); err != nil {
			serverErr <- err
			return
		}
		exception := packet.ErrorResponseTCP{
			TransactionID: binary.BigEndian.Uint16(request[0:2]),
			UnitID:        request[6],
			Function:      request[7],
			Code:          packet.ErrIllegalDataAddress,
		}
		_, err := serverConn.Write(exception.Bytes())
		serverErr <- err
	}()

	config := validModbusTCPConfig()
	config.Timeout = 500
	client, err := newModbusTCPClient(
		config,
		func(context.Context, string) (net.Conn, error) {
			return clientConn, nil
		},
	)
	if err != nil {
		t.Fatalf("newModbusTCPClient() error = %v", err)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	_, err = client.Read(context.Background(), validReadRequest())
	var exception *packet.ErrorResponseTCP
	if !errors.As(err, &exception) {
		t.Fatalf("Read() error = %v, want *packet.ErrorResponseTCP in error chain", err)
	}
	if exception.ErrorCode() != packet.ErrIllegalDataAddress {
		t.Fatalf("exception code = %v, want %v", exception.ErrorCode(), packet.ErrIllegalDataAddress)
	}
	if !client.IsConnected() {
		t.Fatal("IsConnected() = false after Modbus exception, want lifecycle unchanged")
	}
	if serverErr := <-serverErr; serverErr != nil {
		t.Fatalf("simulated Modbus server error = %v", serverErr)
	}
}

func TestModbusTCPClientReadReturnsOwnedBuffer(t *testing.T) {
	t.Parallel()

	responseData := []byte{0x12, 0x34}
	transport := &fakeModbusTransport{
		doFunc: func(context.Context, packet.Request) (packet.Response, error) {
			return &packet.ReadHoldingRegistersResponseTCP{
				ReadHoldingRegistersResponse: packet.ReadHoldingRegistersResponse{Data: responseData},
			}, nil
		},
	}
	client := newConnectedTestModbusTCPClient(transport)

	got, err := client.Read(context.Background(), validReadRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	got[0] = 0xFF
	if responseData[0] != 0x12 {
		t.Fatalf("transport response data mutated to %v, want independent copy", responseData)
	}
}

func TestModbusTCPClientReadRequiresConnection(t *testing.T) {
	t.Parallel()

	transport := &fakeModbusTransport{}
	client := newTestModbusTCPClient(transport)

	data, err := client.Read(context.Background(), validReadRequest())
	if data != nil {
		t.Fatalf("Read() data = %v, want nil", data)
	}
	if !errors.Is(err, ErrModbusNotConnected) {
		t.Fatalf("Read() error = %v, want ErrModbusNotConnected", err)
	}
	if got := transport.doCallCount(); got != 0 {
		t.Fatalf("transport Do() calls = %d, want 0", got)
	}
}

func TestModbusTCPClientReadRejectsInvalidRequestBeforeTransport(t *testing.T) {
	t.Parallel()

	transport := &fakeModbusTransport{}
	client := newConnectedTestModbusTCPClient(transport)

	data, err := client.Read(context.Background(), ReadRequest{
		FunctionCode: FunctionReadHoldingRegisters,
		Quantity:     0,
	})
	if data != nil {
		t.Fatalf("Read() data = %v, want nil", data)
	}
	if !errors.Is(err, ErrInvalidModbusRequest) {
		t.Fatalf("Read() error = %v, want ErrInvalidModbusRequest", err)
	}
	if got := transport.doCallCount(); got != 0 {
		t.Fatalf("transport Do() calls = %d, want 0", got)
	}
}

func TestModbusTCPClientReadWrapsTransportErrorAndKeepsState(t *testing.T) {
	t.Parallel()

	readErr := errors.New("device did not respond")
	transport := &fakeModbusTransport{
		doFunc: func(context.Context, packet.Request) (packet.Response, error) {
			return nil, readErr
		},
	}
	client := newConnectedTestModbusTCPClient(transport)

	data, err := client.Read(context.Background(), validReadRequest())
	if data != nil {
		t.Fatalf("Read() data = %v, want nil", data)
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("Read() error = %v, want wrapped %v", err, readErr)
	}
	if !strings.Contains(err.Error(), client.address) {
		t.Fatalf("Read() error = %q, want address %q", err, client.address)
	}
	if !client.IsConnected() {
		t.Fatal("IsConnected() = false after read error, want lifecycle unchanged")
	}
}

func TestModbusTCPClientReadHonorsContext(t *testing.T) {
	t.Parallel()

	t.Run("applies configured timeout", func(t *testing.T) {
		t.Parallel()

		transport := &fakeModbusTransport{
			doFunc: func(ctx context.Context, _ packet.Request) (packet.Response, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		client := newConnectedTestModbusTCPClient(transport)
		client.timeout = 20 * time.Millisecond

		started := time.Now()
		_, err := client.Read(context.Background(), validReadRequest())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Read() error = %v, want context deadline exceeded", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("Read() took %v, want less than 1s", elapsed)
		}
	})

	t.Run("preserves parent cancellation", func(t *testing.T) {
		t.Parallel()

		transport := &fakeModbusTransport{
			doFunc: func(ctx context.Context, _ packet.Request) (packet.Response, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		client := newConnectedTestModbusTCPClient(transport)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.Read(ctx, validReadRequest())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read() error = %v, want context canceled", err)
		}
	})
}

func TestModbusTCPClientReadRejectsUnexpectedResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response packet.Response
	}{
		{name: "nil response", response: nil},
		{
			name: "wrong response type",
			response: &packet.ReadCoilsResponseTCP{
				ReadCoilsResponse: packet.ReadCoilsResponse{Data: []byte{1}},
			},
		},
		{
			name:     "typed nil response",
			response: (*packet.ReadHoldingRegistersResponseTCP)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &fakeModbusTransport{
				doFunc: func(context.Context, packet.Request) (packet.Response, error) {
					return tt.response, nil
				},
			}
			client := newConnectedTestModbusTCPClient(transport)

			data, err := client.Read(context.Background(), validReadRequest())
			if data != nil {
				t.Fatalf("Read() data = %v, want nil", data)
			}
			if !errors.Is(err, ErrUnexpectedModbusResponse) {
				t.Fatalf("Read() error = %v, want ErrUnexpectedModbusResponse", err)
			}
		})
	}
}

func TestModbusTCPClientSerializesConcurrentReads(t *testing.T) {
	transport := &fakeModbusTransport{}
	var (
		activityMu sync.Mutex
		active     int
		maxActive  int
	)
	transport.doFunc = func(context.Context, packet.Request) (packet.Response, error) {
		activityMu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		activityMu.Unlock()

		time.Sleep(5 * time.Millisecond)

		activityMu.Lock()
		active--
		activityMu.Unlock()
		return &packet.ReadHoldingRegistersResponseTCP{
			ReadHoldingRegistersResponse: packet.ReadHoldingRegistersResponse{Data: []byte{0, 1}},
		}, nil
	}
	client := newConnectedTestModbusTCPClient(transport)

	runConcurrently(t, 16, func() error {
		_, err := client.Read(context.Background(), validReadRequest())
		return err
	})

	activityMu.Lock()
	gotMaxActive := maxActive
	activityMu.Unlock()
	if gotMaxActive != 1 {
		t.Fatalf("maximum concurrent transport reads = %d, want 1", gotMaxActive)
	}
	if got := transport.doCallCount(); got != 16 {
		t.Fatalf("transport Do() calls = %d, want 16", got)
	}
}

func validReadRequest() ReadRequest {
	return ReadRequest{
		UnitID:       17,
		FunctionCode: FunctionReadHoldingRegisters,
		Address:      321,
		Quantity:     1,
	}
}

func newConnectedTestModbusTCPClient(transport modbusTransport) *modbusTCPClient {
	client := newTestModbusTCPClient(transport)
	client.connected = true
	return client
}

func readResponseData(t *testing.T, response packet.Response) []byte {
	t.Helper()

	switch typed := response.(type) {
	case *packet.ReadCoilsResponseTCP:
		return typed.Data
	case *packet.ReadDiscreteInputsResponseTCP:
		return typed.Data
	case *packet.ReadHoldingRegistersResponseTCP:
		return typed.Data
	case *packet.ReadInputRegistersResponseTCP:
		return typed.Data
	default:
		t.Fatalf("unsupported test response type %T", response)
		return nil
	}
}

func assertReadPacket(t *testing.T, got packet.Request, wantType any, want ReadRequest) {
	t.Helper()

	if got == nil {
		t.Fatal("transport request = nil")
	}
	if fmt.Sprintf("%T", got) != fmt.Sprintf("%T", wantType) {
		t.Fatalf("transport request type = %T, want %T", got, wantType)
	}

	wire := got.Bytes()
	if len(wire) != 12 {
		t.Fatalf("wire packet length = %d, want 12", len(wire))
	}
	if wire[6] != want.UnitID {
		t.Fatalf("wire unit ID = %d, want %d", wire[6], want.UnitID)
	}
	if wire[7] != uint8(want.FunctionCode) {
		t.Fatalf("wire function code = %d, want %d", wire[7], want.FunctionCode)
	}
	if gotAddress := binary.BigEndian.Uint16(wire[8:10]); gotAddress != want.Address {
		t.Fatalf("wire address = %d, want %d", gotAddress, want.Address)
	}
	if gotQuantity := binary.BigEndian.Uint16(wire[10:12]); gotQuantity != want.Quantity {
		t.Fatalf("wire quantity = %d, want %d", gotQuantity, want.Quantity)
	}
}

func serveSingleModbusRead(
	conn net.Conn,
	want ReadRequest,
	responseData []byte,
	result chan<- error,
) {
	request := make([]byte, 12)
	if _, err := io.ReadFull(conn, request); err != nil {
		result <- fmt.Errorf("read request: %w", err)
		return
	}
	if request[6] != want.UnitID {
		result <- fmt.Errorf("unit ID = %d, want %d", request[6], want.UnitID)
		return
	}
	if request[7] != uint8(want.FunctionCode) {
		result <- fmt.Errorf("function code = %d, want %d", request[7], want.FunctionCode)
		return
	}
	if got := binary.BigEndian.Uint16(request[8:10]); got != want.Address {
		result <- fmt.Errorf("address = %d, want %d", got, want.Address)
		return
	}
	if got := binary.BigEndian.Uint16(request[10:12]); got != want.Quantity {
		result <- fmt.Errorf("quantity = %d, want %d", got, want.Quantity)
		return
	}

	response := make([]byte, 9+len(responseData))
	copy(response[0:2], request[0:2])
	binary.BigEndian.PutUint16(response[4:6], uint16(3+len(responseData)))
	response[6] = want.UnitID
	response[7] = uint8(want.FunctionCode)
	response[8] = byte(len(responseData))
	copy(response[9:], responseData)

	if _, err := conn.Write(response); err != nil {
		result <- fmt.Errorf("write response: %w", err)
		return
	}
	result <- nil
}
