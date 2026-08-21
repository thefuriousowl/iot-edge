package vgatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestConnect_ReturnsConnectedStatus(t *testing.T) {
	id := uuid.New()
	stub := &serviceStub{connect: func(_ context.Context, gotID uuid.UUID) error {
		if gotID != id {
			t.Errorf("id = %s, want %s", gotID, id)
		}
		return nil
	}}

	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+id.String()+"/connect", "")
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		Message string                            `json:"message"`
		Status  vgateway.VGatewayConnectionStatus `json:"status"`
	}
	decodeResponse(t, response, &body)
	if body.Message != "Connected successfully" || body.Status != vgateway.VGatewayStatusConnected {
		t.Errorf("response = %#v, want connected success", body)
	}
}

func TestConnect_ValidatesIDAndMapsDisabled(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid ID", path: "/api/vgateways/not-a-uuid/connect", wantStatus: fiber.StatusBadRequest, wantCode: "VALIDATION_ERROR"},
		{name: "disabled", path: "/api/vgateways/" + uuid.NewString() + "/connect", serviceErr: vgateway.ErrVGatewayDisabled, wantStatus: fiber.StatusConflict, wantCode: "VGW014"},
		{name: "not found", path: "/api/vgateways/" + uuid.NewString() + "/connect", serviceErr: vgateway.ErrVGatewayNotFound, wantStatus: fiber.StatusNotFound, wantCode: "NOT_FOUND"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{connect: func(context.Context, uuid.UUID) error {
				return test.serviceErr
			}}
			response := performRequest(t, testApp(stub), http.MethodPost, test.path, "")
			defer response.Body.Close()
			assertAPIError(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func TestConnect_MapsNetworkFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "refused", err: errors.Join(errors.New("dial"), syscall.ECONNREFUSED), wantCode: "VGW001"},
		{name: "timeout", err: timeoutError{}, wantCode: "VGW002"},
		{name: "host unreachable", err: errors.Join(errors.New("dial"), syscall.EHOSTUNREACH), wantCode: "VGW003"},
		{name: "network unreachable", err: errors.Join(errors.New("dial"), syscall.ENETUNREACH), wantCode: "VGW003"},
		{name: "DNS", err: &net.DNSError{Err: "no such host", Name: "plc.invalid"}, wantCode: "VGW004"},
		{name: "closed", err: net.ErrClosed, wantCode: "VGW005"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{connect: func(context.Context, uuid.UUID) error {
				return test.err
			}}
			response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/connect", "")
			defer response.Body.Close()
			assertAPIError(t, response, fiber.StatusInternalServerError, test.wantCode)
		})
	}
}

func TestDisconnect_ReturnsProjectedStatus(t *testing.T) {
	id := uuid.New()
	disconnectCalls := 0
	statusCalls := 0
	stub := &serviceStub{
		disconnect: func(_ context.Context, gotID uuid.UUID) error {
			disconnectCalls++
			if gotID != id {
				t.Errorf("disconnect id = %s, want %s", gotID, id)
			}
			return nil
		},
		status: func(_ context.Context, gotID uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
			statusCalls++
			if gotID != id {
				t.Errorf("status id = %s, want %s", gotID, id)
			}
			return &vgateway.VGatewayStatusResult{ID: id, Status: vgateway.VGatewayStatusStopped}, nil
		},
	}

	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+id.String()+"/disconnect", "")
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		Message string                            `json:"message"`
		Status  vgateway.VGatewayConnectionStatus `json:"status"`
	}
	decodeResponse(t, response, &body)
	if body.Message != "Disconnected successfully" || body.Status != vgateway.VGatewayStatusStopped {
		t.Errorf("response = %#v, want stopped disconnect success", body)
	}
	if disconnectCalls != 1 || statusCalls != 1 {
		t.Errorf("calls = disconnect %d, status %d; want one each", disconnectCalls, statusCalls)
	}
}

func TestDisconnect_DoesNotReadStatusAfterFailure(t *testing.T) {
	stub := &serviceStub{
		disconnect: func(context.Context, uuid.UUID) error {
			return vgateway.ErrVGatewayNotFound
		},
		status: func(context.Context, uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
			t.Fatal("Status called after failed disconnect")
			return nil, nil
		},
	}
	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/disconnect", "")
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusNotFound, "NOT_FOUND")
}

func TestDisconnect_RejectsNilStatusResult(t *testing.T) {
	stub := &serviceStub{
		disconnect: func(context.Context, uuid.UUID) error { return nil },
		status: func(context.Context, uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
			return nil, nil
		},
	}
	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/disconnect", "")
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusInternalServerError, "INTERNAL_ERROR")
}

func TestTestConnection_ForwardsOptionsAndReturnsFractionalLatency(t *testing.T) {
	id := uuid.New()
	stub := &serviceStub{testConnection: func(_ context.Context, gotID uuid.UUID, options json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error) {
		if gotID != id {
			t.Errorf("id = %s, want %s", gotID, id)
		}
		if string(options) != `{"unit_id":7}` {
			t.Errorf("options = %s, want unit_id 7", options)
		}
		return &vgateway.VGatewayConnectionTestResult{Success: true, Latency: 1250 * time.Microsecond}, nil
	}}

	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+id.String()+"/test", `{"unit_id":7}`)
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		Success   bool    `json:"success"`
		LatencyMS float64 `json:"latency_ms"`
		Message   string  `json:"message"`
	}
	decodeResponse(t, response, &body)
	if !body.Success || body.LatencyMS != 1.25 || body.Message != "Connection successful" {
		t.Errorf("response = %#v, want successful 1.25ms test", body)
	}
}

func TestTestConnection_AllowsEmptyBody(t *testing.T) {
	stub := &serviceStub{testConnection: func(_ context.Context, _ uuid.UUID, options json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error) {
		if len(options) != 0 {
			t.Errorf("options = %q, want empty", options)
		}
		return &vgateway.VGatewayConnectionTestResult{Success: true}, nil
	}}

	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/test", "")
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
}

func TestTestConnection_ReturnsSanitizedFailure(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantFailure string
		wantMessage string
	}{
		{name: "refused", err: errors.Join(errors.New("private target 10.0.0.5"), syscall.ECONNREFUSED), wantFailure: "Connection refused", wantMessage: "Unable to connect. Please check host and port."},
		{name: "unknown", err: errors.New("private driver detail"), wantFailure: "Connection failed", wantMessage: "Unable to connect to the vGateway."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{testConnection: func(context.Context, uuid.UUID, json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error) {
				return &vgateway.VGatewayConnectionTestResult{Success: false, Error: test.err}, nil
			}}
			response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/test", "")
			defer response.Body.Close()
			var body struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			decodeResponse(t, response, &body)
			if body.Success || body.Error != test.wantFailure || body.Message != test.wantMessage {
				t.Errorf("response = %#v, want sanitized failure", body)
			}
		})
	}
}

func TestTestConnection_RejectsMalformedBodyAndInvalidOptions(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantCode   string
	}{
		{name: "malformed", body: `{"unit_id":`, wantCode: "VALIDATION_ERROR"},
		{name: "multiple documents", body: `{}` + ` {}`, wantCode: "VALIDATION_ERROR"},
		{name: "invalid options", body: `{"unit_id":256}`, serviceErr: vgateway.ErrInvalidVGatewayTestInput, wantCode: "VGW010"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{testConnection: func(context.Context, uuid.UUID, json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error) {
				return nil, test.serviceErr
			}}
			response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/test", test.body)
			defer response.Body.Close()
			assertAPIError(t, response, fiber.StatusBadRequest, test.wantCode)
		})
	}
}

func TestTestConnection_RejectsNilServiceResult(t *testing.T) {
	stub := &serviceStub{testConnection: func(context.Context, uuid.UUID, json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error) {
		return nil, nil
	}}
	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways/"+uuid.NewString()+"/test", "")
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusInternalServerError, "INTERNAL_ERROR")
}

func TestStatus_ReturnsFullRuntimeProjection(t *testing.T) {
	id := uuid.New()
	connectedAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	latency := 12.5
	stub := &serviceStub{status: func(_ context.Context, gotID uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
		if gotID != id {
			t.Errorf("id = %s, want %s", gotID, id)
		}
		return &vgateway.VGatewayStatusResult{
			ID:          id,
			Status:      vgateway.VGatewayStatusConnected,
			ConnectedAt: &connectedAt,
			Statistics: vgateway.VGatewayStatusStatistics{
				RequestCount: 10,
				ErrorCount:   1,
				AvgLatencyMS: &latency,
			},
			Health: vgateway.VGatewayHealth{Status: vgateway.VGatewayHealthUnknown},
		}, nil
	}}

	response := performRequest(t, testApp(stub), http.MethodGet, "/api/vgateways/"+id.String()+"/status", "")
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body map[string]any
	decodeResponse(t, response, &body)
	if body["id"] != id.String() || body["status"] != "connected" || body["connected_at"] != connectedAt.Format(time.RFC3339) {
		t.Errorf("response = %#v, want connected status projection", body)
	}
	statistics, ok := body["statistics"].(map[string]any)
	if !ok || statistics["request_count"] != float64(10) || statistics["avg_latency_ms"] != 12.5 {
		t.Errorf("statistics = %#v, want projected counters", body["statistics"])
	}
}

func TestStatus_ValidatesIDAndMapsNotFound(t *testing.T) {
	tests := []struct {
		path       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{path: "/api/vgateways/not-a-uuid/status", wantStatus: fiber.StatusBadRequest, wantCode: "VALIDATION_ERROR"},
		{path: "/api/vgateways/" + uuid.NewString() + "/status", serviceErr: vgateway.ErrVGatewayNotFound, wantStatus: fiber.StatusNotFound, wantCode: "NOT_FOUND"},
	}

	for _, test := range tests {
		stub := &serviceStub{status: func(context.Context, uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
			return nil, test.serviceErr
		}}
		response := performRequest(t, testApp(stub), http.MethodGet, test.path, "")
		defer response.Body.Close()
		assertAPIError(t, response, test.wantStatus, test.wantCode)
	}
}

func TestStatus_RejectsNilServiceResult(t *testing.T) {
	stub := &serviceStub{status: func(context.Context, uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
		return nil, nil
	}}
	response := performRequest(t, testApp(stub), http.MethodGet, "/api/vgateways/"+uuid.NewString()+"/status", "")
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusInternalServerError, "INTERNAL_ERROR")
}
