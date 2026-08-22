package modbus

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/aldas/go-modbus-client/packet"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

type requestError struct {
	cause   error
	message string
	details map[string]any
}

func (e *requestError) Error() string { return e.message }

func (e *requestError) Unwrap() error { return e.cause }

func (e *requestError) PublicMessage() string { return e.message }

func (e *requestError) PublicDetails() map[string]any {
	details := make(map[string]any, len(e.details))
	for key, value := range e.details {
		details[key] = value
	}
	return details
}

func classifyRequestError(err error, request ReadRequest) error {
	if err == nil {
		return nil
	}
	var publicError protocol.RequestError
	if errors.As(err, &publicError) {
		return err
	}

	var exception packet.ModbusError
	if errors.As(err, &exception) {
		return newModbusExceptionError(err, request, exception.ErrorCode())
	}

	details := requestDetails(request)
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		details["failure_type"] = "timeout"
		return &requestError{
			cause:   err,
			message: "Modbus request timed out. Check server availability and the configured request timeout.",
			details: details,
		}
	}

	details["failure_type"] = "request_failed"
	return &requestError{
		cause:   err,
		message: "Modbus request failed. Check the server connection, Unit ID, function code, address, and quantity.",
		details: details,
	}
}

func newModbusExceptionError(cause error, request ReadRequest, code packet.ErrCode) error {
	name, title, explanation := modbusExceptionDescription(code, request)
	details := requestDetails(request)
	details["failure_type"] = "exception"
	details["exception_code"] = uint8(code)
	details["exception_hex"] = fmt.Sprintf("0x%02X", uint8(code))
	details["exception_name"] = name
	return &requestError{
		cause:   cause,
		message: fmt.Sprintf("Modbus exception 0x%02X: %s. %s", uint8(code), title, explanation),
		details: details,
	}
}

func requestDetails(request ReadRequest) map[string]any {
	return map[string]any{
		"protocol":      "modbus_tcp",
		"unit_id":       request.UnitID,
		"function_code": uint8(request.FunctionCode),
		"start_address": request.Address,
		"quantity":      request.Quantity,
	}
}

func modbusExceptionDescription(code packet.ErrCode, request ReadRequest) (string, string, string) {
	lastAddress := uint32(request.Address) + uint32(request.Quantity) - 1
	switch code {
	case packet.ErrIllegalFunction:
		return "ILLEGAL_FUNCTION", "Illegal Function", fmt.Sprintf("The server does not allow function code FC%02d for this request.", request.FunctionCode)
	case packet.ErrIllegalDataAddress:
		return "ILLEGAL_DATA_ADDRESS", "Illegal Data Address", fmt.Sprintf("The requested address range %d-%d is not available on the server.", request.Address, lastAddress)
	case packet.ErrIllegalDataValue:
		return "ILLEGAL_DATA_VALUE", "Illegal Data Value", "The server rejected a value or length in the request."
	case packet.ErrServerFailure:
		return "SERVER_DEVICE_FAILURE", "Server Device Failure", "The server encountered an unrecoverable error while processing the request."
	case packet.ErrAcknowledge:
		return "ACKNOWLEDGE", "Acknowledge", "The server accepted the request and is still processing it."
	case packet.ErrServerBusy:
		return "SERVER_DEVICE_BUSY", "Server Device Busy", "The server is busy processing another long-running command; retry later."
	case packet.ErrMemoryParityError:
		return "MEMORY_PARITY_ERROR", "Memory Parity Error", "The server detected a memory consistency error while processing the request."
	case packet.ErrGatewayPathUnavailable:
		return "GATEWAY_PATH_UNAVAILABLE", "Gateway Path Unavailable", "The gateway could not allocate a path to the target device."
	case packet.ErrGatewayTargetedDeviceResponse:
		return "GATEWAY_TARGET_DEVICE_FAILED_TO_RESPOND", "Gateway Target Device Failed to Respond", "The gateway did not receive a response from the target device."
	default:
		return "UNKNOWN_EXCEPTION", "Unknown Exception", "The server returned an unrecognized Modbus exception code."
	}
}

func isModbusException(err error) bool {
	var exception packet.ModbusError
	return errors.As(err, &exception)
}
