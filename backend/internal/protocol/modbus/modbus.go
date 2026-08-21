package modbus

import (
	"context"

	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

type FunctionCode uint8

const (
	FunctionReadCoils            FunctionCode = 1
	FunctionReadDiscreteInputs   FunctionCode = 2
	FunctionReadHoldingRegisters FunctionCode = 3
	FunctionReadInputRegisters   FunctionCode = 4
)

type ReadRequest struct {
	UnitID       uint8
	FunctionCode FunctionCode
	Address      uint16
	Quantity     uint16
}

type ModbusClient interface {
	protocol.GatewayClient
	Read(ctx context.Context, request ReadRequest) ([]byte, error)
}
