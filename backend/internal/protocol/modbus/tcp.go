package modbus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	modbus "github.com/aldas/go-modbus-client"
	"github.com/aldas/go-modbus-client/packet"
)

var (
	ErrInvalidModbusConfig = errors.New(
		"invalid Modbus TCP config",
	)
	ErrInvalidModbusRequest = errors.New(
		"invalid Modbus read request",
	)
	ErrModbusNotConnected = errors.New(
		"Modbus TCP client is not connected",
	)
	ErrUnexpectedModbusResponse = errors.New(
		"unexpected Modbus response",
	)
)

type modbusTransport interface {
	Connect(context.Context, string) error
	Close() error
	Do(context.Context, packet.Request) (packet.Response, error)
}

type dialContextFunc func(
	ctx context.Context,
	address string,
) (net.Conn, error)

type modbusTCPClient struct {
	mu        sync.Mutex
	address   string
	timeout   time.Duration
	connected bool
	transport modbusTransport
}

var _ ModbusClient = (*modbusTCPClient)(nil)

func NewModbusTCPClient(
	config ModbusTCPConfig,
) (ModbusClient, error) {
	return newModbusTCPClient(config, nil)
}

func newModbusTCPClient(
	config ModbusTCPConfig,
	dial dialContextFunc,
) (*modbusTCPClient, error) {

	host := strings.TrimSpace(config.Host)

	if host == "" {
		return nil, fmt.Errorf(
			"%w: host is required",
			ErrInvalidModbusConfig,
		)
	}

	if config.Port < 1 || config.Port > 65535 {
		return nil, fmt.Errorf(
			"%w: port must be between 1 and 65535",
			ErrInvalidModbusConfig,
		)
	}

	if config.Timeout < 100 || config.Timeout > 60000 {
		return nil, fmt.Errorf(
			"%w: timeout must be between 100 and 60000 milliseconds",
			ErrInvalidModbusConfig,
		)
	}

	timeout := time.Duration(config.Timeout) * time.Millisecond

	if dial == nil {
		dial = newDefaultDialer(timeout, config.KeepAlive)
	}

	transport := modbus.NewTCPClientWithConfig(modbus.ClientConfig{
		ReadTimeout:     timeout,
		WriteTimeout:    timeout,
		DialContextFunc: dial,
	})

	return &modbusTCPClient{
		address: net.JoinHostPort(
			host,
			strconv.Itoa(config.Port),
		),
		timeout:   timeout,
		transport: transport,
	}, nil
}

func newDefaultDialer(
	timeout time.Duration,
	keepAlive bool,
) dialContextFunc {
	keepAliveInterval := 15 * time.Second
	if !keepAlive {
		keepAliveInterval = -1
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: keepAliveInterval,
	}

	return func(
		ctx context.Context,
		address string,
	) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", address)
	}
}

func (c *modbusTCPClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if err := c.transport.Connect(connectCtx, c.address); err != nil {
		return fmt.Errorf(
			"connect to Modbus TCP gateway %s: %w",
			c.address,
			err,
		)
	}

	c.connected = true
	return nil
}

func (c *modbusTCPClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	err := c.transport.Close()

	// Client.Close() clears its connection even when the underlying
	// net.Conn.Close() returns an error.
	c.connected = false

	if err != nil {
		return fmt.Errorf(
			"disconnect from Modbus TCP gateway %s: %w",
			c.address,
			err,
		)
	}

	return nil
}

func (c *modbusTCPClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.connected
}

func (c *modbusTCPClient) Read(
	ctx context.Context,
	request ReadRequest,
) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateReadRequest(request); err != nil {
		return nil, err
	}

	if !c.connected {
		return nil, ErrModbusNotConnected
	}

	readCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	wireRequest, err := buildReadPacket(request)
	if err != nil {
		return nil, fmt.Errorf(
			"build Modbus read request: %w",
			err,
		)
	}

	response, err := c.transport.Do(readCtx, wireRequest)
	if err != nil {
		return nil, fmt.Errorf(
			"read Modbus TCP gateway %s: %w",
			c.address,
			err,
		)
	}

	data, err := extractReadData(
		request.FunctionCode,
		response,
	)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func validateReadRequest(request ReadRequest) error {
	if request.Quantity == 0 {
		return fmt.Errorf(
			"%w: quantity must be greater than zero",
			ErrInvalidModbusRequest,
		)
	}

	var maxQuantity uint16

	switch request.FunctionCode {
	case FunctionReadCoils,
		FunctionReadDiscreteInputs:
		maxQuantity = 2000

	case FunctionReadHoldingRegisters,
		FunctionReadInputRegisters:
		maxQuantity = 125

	default:
		return fmt.Errorf(
			"%w: unsupported function code %d",
			ErrInvalidModbusRequest,
			request.FunctionCode,
		)
	}

	if request.Quantity > maxQuantity {
		return fmt.Errorf(
			"%w: quantity %d exceeds maximum %d for function code %d",
			ErrInvalidModbusRequest,
			request.Quantity,
			maxQuantity,
			request.FunctionCode,
		)
	}

	lastAddress :=
		uint32(request.Address) +
			uint32(request.Quantity) -
			1

	if lastAddress > 65535 {
		return fmt.Errorf(
			"%w: address range %d-%d exceeds maximum address 65535",
			ErrInvalidModbusRequest,
			request.Address,
			lastAddress,
		)
	}

	return nil
}

func buildReadPacket(
	request ReadRequest,
) (packet.Request, error) {
	switch request.FunctionCode {
	case FunctionReadCoils:
		return packet.NewReadCoilsRequestTCP(
			request.UnitID,
			request.Address,
			request.Quantity,
		)

	case FunctionReadDiscreteInputs:
		return packet.NewReadDiscreteInputsRequestTCP(
			request.UnitID,
			request.Address,
			request.Quantity,
		)

	case FunctionReadHoldingRegisters:
		return packet.NewReadHoldingRegistersRequestTCP(
			request.UnitID,
			request.Address,
			request.Quantity,
		)

	case FunctionReadInputRegisters:
		return packet.NewReadInputRegistersRequestTCP(
			request.UnitID,
			request.Address,
			request.Quantity,
		)

	default:
		return nil, fmt.Errorf(
			"%w: unsupported function code %d",
			ErrInvalidModbusRequest,
			request.FunctionCode,
		)
	}
}

func extractReadData(
	functionCode FunctionCode,
	response packet.Response,
) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf(
			"%w: received nil response",
			ErrUnexpectedModbusResponse,
		)
	}

	var data []byte

	switch functionCode {
	case FunctionReadCoils:
		typed, ok := response.(*packet.ReadCoilsResponseTCP)
		if !ok || typed == nil {
			return nil, unexpectedResponseError(
				functionCode,
				response,
			)
		}
		data = typed.Data

	case FunctionReadDiscreteInputs:
		typed, ok :=
			response.(*packet.ReadDiscreteInputsResponseTCP)
		if !ok || typed == nil {
			return nil, unexpectedResponseError(
				functionCode,
				response,
			)
		}
		data = typed.Data

	case FunctionReadHoldingRegisters:
		typed, ok :=
			response.(*packet.ReadHoldingRegistersResponseTCP)
		if !ok || typed == nil {
			return nil, unexpectedResponseError(
				functionCode,
				response,
			)
		}
		data = typed.Data

	case FunctionReadInputRegisters:
		typed, ok :=
			response.(*packet.ReadInputRegistersResponseTCP)
		if !ok || typed == nil {
			return nil, unexpectedResponseError(
				functionCode,
				response,
			)
		}
		data = typed.Data

	default:
		return nil, fmt.Errorf(
			"%w: unsupported function code %d",
			ErrUnexpectedModbusResponse,
			functionCode,
		)
	}

	// Do not expose a buffer owned by the transport library.
	return append([]byte(nil), data...), nil
}

func unexpectedResponseError(
	functionCode FunctionCode,
	response packet.Response,
) error {
	return fmt.Errorf(
		"%w: function code %d returned %T",
		ErrUnexpectedModbusResponse,
		functionCode,
		response,
	)
}
