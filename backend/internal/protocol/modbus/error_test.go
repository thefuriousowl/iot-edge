package modbus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aldas/go-modbus-client/packet"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func TestClassifyRequestErrorMapsStandardModbusExceptions(t *testing.T) {
	t.Parallel()
	request := ReadRequest{UnitID: 7, FunctionCode: FunctionReadHoldingRegisters, Address: 96, Quantity: 5}
	tests := []struct {
		code  packet.ErrCode
		name  string
		title string
	}{
		{code: packet.ErrIllegalFunction, name: "ILLEGAL_FUNCTION", title: "Illegal Function"},
		{code: packet.ErrIllegalDataAddress, name: "ILLEGAL_DATA_ADDRESS", title: "Illegal Data Address"},
		{code: packet.ErrIllegalDataValue, name: "ILLEGAL_DATA_VALUE", title: "Illegal Data Value"},
		{code: packet.ErrServerFailure, name: "SERVER_DEVICE_FAILURE", title: "Server Device Failure"},
		{code: packet.ErrAcknowledge, name: "ACKNOWLEDGE", title: "Acknowledge"},
		{code: packet.ErrServerBusy, name: "SERVER_DEVICE_BUSY", title: "Server Device Busy"},
		{code: packet.ErrMemoryParityError, name: "MEMORY_PARITY_ERROR", title: "Memory Parity Error"},
		{code: packet.ErrGatewayPathUnavailable, name: "GATEWAY_PATH_UNAVAILABLE", title: "Gateway Path Unavailable"},
		{code: packet.ErrGatewayTargetedDeviceResponse, name: "GATEWAY_TARGET_DEVICE_FAILED_TO_RESPOND", title: "Gateway Target Device Failed to Respond"},
		{code: packet.ErrCode(12), name: "UNKNOWN_EXCEPTION", title: "Unknown Exception"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cause := &packet.ErrorResponseTCP{UnitID: request.UnitID, Function: uint8(request.FunctionCode), Code: test.code}
			err := classifyRequestError(cause, request)
			if !errors.Is(err, cause) {
				t.Fatalf("classified error does not preserve cause: %v", err)
			}
			message, details, ok := protocol.PublicRequestError(err)
			if !ok {
				t.Fatalf("PublicRequestError() did not recognize %T", err)
			}
			if !strings.Contains(message, "Modbus exception") || !strings.Contains(message, test.title) {
				t.Errorf("message = %q, want standard title %q", message, test.title)
			}
			if details["protocol"] != "modbus_tcp" || details["exception_code"] != uint8(test.code) || details["exception_name"] != test.name {
				t.Errorf("details = %#v", details)
			}
			if details["unit_id"] != uint8(7) || details["function_code"] != uint8(3) || details["start_address"] != uint16(96) || details["quantity"] != uint16(5) {
				t.Errorf("request details = %#v", details)
			}
			details["protocol"] = "mutated"
			_, fresh, _ := protocol.PublicRequestError(err)
			if fresh["protocol"] != "modbus_tcp" {
				t.Error("PublicDetails() exposed mutable internal state")
			}
		})
	}
}

func TestClassifyRequestErrorMapsOperationalFailures(t *testing.T) {
	t.Parallel()
	request := ReadRequest{UnitID: 1, FunctionCode: FunctionReadInputRegisters, Address: 10, Quantity: 2}
	tests := []struct {
		name        string
		cause       error
		failureType string
		message     string
	}{
		{name: "timeout", cause: context.DeadlineExceeded, failureType: "timeout", message: "timed out"},
		{name: "request failure", cause: errors.New("connection refused"), failureType: "request_failed", message: "Modbus request failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := classifyRequestError(test.cause, request)
			message, details, ok := protocol.PublicRequestError(err)
			if !ok || !strings.Contains(message, test.message) || details["failure_type"] != test.failureType {
				t.Fatalf("message/details = %q / %#v", message, details)
			}
			if !errors.Is(err, test.cause) {
				t.Fatalf("classified error does not preserve cause: %v", err)
			}
		})
	}
	if classifyRequestError(nil, request) != nil {
		t.Error("classifyRequestError(nil) did not return nil")
	}
}
