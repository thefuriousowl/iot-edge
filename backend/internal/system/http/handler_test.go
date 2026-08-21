package systemhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/thefuriousowl/iot-edge/internal/system"
)

type connectivityStub struct {
	result system.InternetStatusResult
	calls  int
}

func (s *connectivityStub) Check(context.Context) system.InternetStatusResult {
	s.calls++
	return s.result
}

func TestInternetStatusReturnsConnectivityProjection(t *testing.T) {
	t.Parallel()

	latency := 12.5
	checkedAt := time.Date(2026, 8, 21, 6, 0, 0, 0, time.UTC)
	checker := &connectivityStub{result: system.InternetStatusResult{
		Status:    system.InternetConnectionOnline,
		CheckedAt: checkedAt,
		LatencyMS: &latency,
	}}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(checker))

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/system/internet-status", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body system.InternetStatusResult
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != system.InternetConnectionOnline || body.CheckedAt != checkedAt || body.LatencyMS == nil || *body.LatencyMS != latency {
		t.Errorf("response = %#v, want online projection", body)
	}
	if checker.calls != 1 {
		t.Errorf("Check() calls = %d, want 1", checker.calls)
	}
}

func TestRegisterRoutesOnlyAcceptsGET(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(&connectivityStub{}))
	response, err := app.Test(httptest.NewRequest(http.MethodPost, "/api/system/internet-status", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusMethodNotAllowed)
	}
}
