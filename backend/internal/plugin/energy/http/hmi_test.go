package energyhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin/energy"
)

func TestHMIEnergySnapshotPreservesZeroAndUnavailableSemantics(t *testing.T) {
	pluginID := uuid.New()
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	zero := energy.RatioMetric{Value: 0, Valid: true}
	service := &energyHandlerService{overview: &energy.OverviewResult{
		AsOf:  to,
		Today: energy.PeriodSummary{From: from, To: to, Cost: zero, Electrical: energy.EnergySummary{CoveredSeconds: 3600}},
		Month: energy.PeriodSummary{From: from, To: to, Cost: energy.RatioMetric{Error: "insufficient coverage"}, Electrical: energy.EnergySummary{CoveredSeconds: 3600}},
	}}
	app := fiber.New()
	key := strings.Repeat("k", 32)
	if err := RegisterHMIRoutes(app, NewHandler(service), HMIConfig{PluginInstanceID: pluginID, APIKey: key}); err != nil {
		t.Fatal(err)
	}

	unauthorized, err := app.Test(httptest.NewRequest(http.MethodGet, "/hmi/v1/energy/snapshot", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}
	_ = unauthorized.Body.Close()

	request := httptest.NewRequest(http.MethodGet, "/hmi/v1/energy/snapshot", nil)
	request.Header.Set(HMIAPIKeyHeader, key)
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var snapshot hmiSnapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.TodayEstimatedCost.Value == nil || *snapshot.TodayEstimatedCost.Value != 0 || !snapshot.TodayEstimatedCost.Available {
		t.Fatalf("today cost = %#v, want available real zero", snapshot.TodayEstimatedCost)
	}
	if snapshot.MonthEstimatedCost.Value != nil || snapshot.MonthEstimatedCost.Available || snapshot.MonthEstimatedCost.Reason == "" {
		t.Fatalf("month cost = %#v, want unavailable nil", snapshot.MonthEstimatedCost)
	}
	if snapshot.TodayEstimatedCost.PeriodStart == nil || snapshot.MonthElectricalEnergy.Value == nil {
		t.Fatal("available period metadata or electrical energy missing")
	}
	if snapshot.InstantaneousCOP.Available || snapshot.InstantaneousCOP.Reason != "waiting_for_committed_batch" {
		t.Fatalf("instantaneous COP = %#v", snapshot.InstantaneousCOP)
	}
	if service.overviewID != pluginID {
		t.Fatalf("overview ID = %s", service.overviewID)
	}
}

func TestHMIEnergyResetUsesFixedPluginAndStrictBody(t *testing.T) {
	pluginID, runID := uuid.New(), uuid.New()
	service := &energyHandlerService{run: &energy.MeasurementRun{ID: uuid.New()}}
	app := fiber.New()
	key := strings.Repeat("s", 32)
	if err := RegisterHMIRoutes(app, NewHandler(service), HMIConfig{PluginInstanceID: pluginID, APIKey: key}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/hmi/v1/energy/reset", strings.NewReader(`{"expected_run_id":"`+runID.String()+`","name":"Chiller 2","reason":"relocated"}`))
	request.Header.Set(HMIAPIKeyHeader, key)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if service.historyID != pluginID || service.resetInput.ExpectedRunID != runID || service.resetInput.Name != "Chiller 2" {
		t.Fatalf("reset call = %s %#v", service.historyID, service.resetInput)
	}

	bad := httptest.NewRequest(http.MethodPost, "/hmi/v1/energy/reset", strings.NewReader(`{"expected_run_id":"`+runID.String()+`","name":"x","reason":"","extra":true}`))
	bad.Header.Set(HMIAPIKeyHeader, key)
	badResponse, err := app.Test(bad, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer badResponse.Body.Close()
	if badResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("strict body status = %d", badResponse.StatusCode)
	}
}
