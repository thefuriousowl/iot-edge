package energyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/plugin/energy"
)

func TestEnergyHandlerOverviewHistoryAndExportContracts(t *testing.T) {
	t.Parallel()
	instanceID, loggerID := uuid.New(), uuid.New()
	from := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	service := &energyHandlerService{
		overview: &energy.OverviewResult{InstanceID: instanceID, LoggerID: loggerID, Timezone: "Asia/Bangkok", Currency: "THB", AsOf: to},
		history: &energy.HistoryResult{
			InstanceID: instanceID, LoggerID: loggerID, Timezone: "Asia/Bangkok", Bucket: datalogger.QueryBucket15Minutes,
			RequestedBucket: datalogger.QueryBucket1Minute, Downsampled: true, PointLimit: energy.MaxHistoryPoints,
			Currency: "THB", RatePerKWh: 4.5, Data: []energy.HistoryRow{{PeriodSummary: energy.PeriodSummary{From: from, To: to}}},
			Page: 2, PerPage: 5, Total: 6, TotalPages: 2,
		},
		export: "from,to\n2026-08-23T07:00:00+07:00,2026-08-23T08:00:00+07:00\n",
	}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))

	response := energyRequest(t, app, "/api/plugins/"+instanceID.String()+"/energy/overview")
	assertEnergyStatus(t, response, fiber.StatusOK)
	var overview energy.OverviewResult
	decodeEnergyResponse(t, response, &overview)
	if overview.InstanceID != instanceID || overview.LoggerID != loggerID || service.overviewID != instanceID {
		t.Errorf("Overview response/input = %#v / %s", overview, service.overviewID)
	}

	query := url.Values{
		"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}, "bucket": {string(datalogger.QueryBucket15Minutes)}, "page": {"2"}, "per_page": {"5"},
	}
	response = energyRequest(t, app, "/api/plugins/"+instanceID.String()+"/energy/history?"+query.Encode())
	assertEnergyStatus(t, response, fiber.StatusOK)
	var history struct {
		InstanceID uuid.UUID           `json:"instance_id"`
		Data       []energy.HistoryRow `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			PerPage    int `json:"per_page"`
			Total      int `json:"total"`
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
		RequestedBucket datalogger.QueryBucket `json:"requested_bucket"`
		Downsampled     bool                   `json:"downsampled"`
		PointLimit      int                    `json:"point_limit"`
	}
	decodeEnergyResponse(t, response, &history)
	if service.historyID != instanceID || service.input.From != from || service.input.To != to || service.input.Bucket != datalogger.QueryBucket15Minutes || service.input.Page != 2 || service.input.PerPage != 5 {
		t.Errorf("History() input = %s, %#v", service.historyID, service.input)
	}
	if history.InstanceID != instanceID || len(history.Data) != 1 || history.Pagination.Page != 2 || history.Pagination.Total != 6 || history.Pagination.TotalPages != 2 || history.RequestedBucket != datalogger.QueryBucket1Minute || !history.Downsampled || history.PointLimit != energy.MaxHistoryPoints {
		t.Errorf("History response = %#v", history)
	}

	response = energyRequest(t, app, "/api/plugins/"+instanceID.String()+"/energy/export.csv?"+query.Encode())
	assertEnergyStatus(t, response, fiber.StatusOK)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading CSV: %v", err)
	}
	_ = response.Body.Close()
	if string(body) != service.export || response.Header.Get("Content-Type") != "text/csv; charset=utf-8" || !strings.Contains(response.Header.Get("Content-Disposition"), instanceID.String()) || service.validateID != instanceID || service.exportID != instanceID {
		t.Errorf("CSV response = headers %#v, body %q", response.Header, body)
	}
}

func TestEnergyHandlerRejectsInvalidQueriesAndSanitizesErrors(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	from := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	validQuery := "?from=" + url.QueryEscape(from.Format(time.RFC3339)) + "&to=" + url.QueryEscape(from.Add(time.Hour).Format(time.RFC3339))
	tests := []struct {
		name   string
		path   string
		err    error
		status int
		code   string
	}{
		{name: "invalid ID", path: "/api/plugins/not-a-uuid/energy/overview", status: fiber.StatusBadRequest, code: "ENG000"},
		{name: "missing range", path: "/api/plugins/" + instanceID.String() + "/energy/history", status: fiber.StatusBadRequest, code: "ENG000"},
		{name: "invalid page", path: "/api/plugins/" + instanceID.String() + "/energy/history" + validQuery + "&page=0", status: fiber.StatusBadRequest, code: "ENG000"},
		{name: "not found", path: "/api/plugins/" + instanceID.String() + "/energy/overview", err: plugin.ErrInstanceNotFound, status: fiber.StatusNotFound, code: "ENG001"},
		{name: "wrong type", path: "/api/plugins/" + instanceID.String() + "/energy/overview", err: energy.ErrEnergyInstanceRequired, status: fiber.StatusConflict, code: "ENG002"},
		{name: "bad config", path: "/api/plugins/" + instanceID.String() + "/energy/overview", err: energy.ErrInvalidConfiguration, status: fiber.StatusConflict, code: "ENG003"},
		{name: "invalid query", path: "/api/plugins/" + instanceID.String() + "/energy/history" + validQuery, err: energy.ErrInvalidHistoryQuery, status: fiber.StatusBadRequest, code: "ENG000"},
		{name: "internal", path: "/api/plugins/" + instanceID.String() + "/energy/overview", err: errors.New("postgres password=do-not-expose"), status: fiber.StatusInternalServerError, code: "ENG500"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &energyHandlerService{err: test.err}
			app := fiber.New()
			RegisterRoutes(app.Group("/api"), NewHandler(service))
			response := energyRequest(t, app, test.path)
			assertEnergyStatus(t, response, test.status)
			var body struct {
				Error struct {
					Code, Message string
				} `json:"error"`
			}
			decodeEnergyResponse(t, response, &body)
			if body.Error.Code != test.code || strings.Contains(body.Error.Message, "password") {
				t.Errorf("error response = %#v", body)
			}
		})
	}
}

func TestEnergyHandlerValidatesExportBeforeStreaming(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	from := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	service := &energyHandlerService{validateErr: plugin.ErrInstanceNotFound}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	path := "/api/plugins/" + instanceID.String() + "/energy/export.csv?from=" + url.QueryEscape(from.Format(time.RFC3339)) + "&to=" + url.QueryEscape(from.Add(time.Hour).Format(time.RFC3339))
	response := energyRequest(t, app, path)
	assertEnergyStatus(t, response, fiber.StatusNotFound)
	if service.exportID != uuid.Nil {
		t.Errorf("ExportCSV() called after validation failure for %s", service.exportID)
	}
	_ = response.Body.Close()
}

func TestEnergyHandlerStreamsResetReplayAndLiveMetrics(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	at := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	session := uuid.NewString()
	replay := energy.LiveEvent{ID: session + ":10", Sequence: 10, InstanceID: instanceID, Metrics: energy.BatchMetrics{BatchAt: at, Electrical: energy.DemandMetric{Kilowatts: 2, Valid: true}}}
	live := energy.LiveEvent{ID: session + ":11", Sequence: 11, InstanceID: instanceID, Metrics: energy.BatchMetrics{BatchAt: at.Add(time.Second), Electrical: energy.DemandMetric{Kilowatts: 3, Valid: true}}}
	stream := make(chan energy.LiveEvent, 1)
	stream <- live
	close(stream)
	service := &energyHandlerService{live: &energy.LiveSubscription{Replay: []energy.LiveEvent{replay}, Stream: stream, Reset: true}}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	request := httptest.NewRequest(http.MethodGet, "/api/plugins/"+instanceID.String()+"/energy/stream", nil)
	request.Header.Set("Last-Event-ID", uuid.NewString()+":5")
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	assertEnergyStatus(t, response, fiber.StatusOK)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading SSE: %v", err)
	}
	_ = response.Body.Close()
	contents := string(body)
	for _, expected := range []string{"retry: 3000", "event: energy_reset", "id: " + replay.ID, "id: " + live.ID, "event: energy_metrics", `"kilowatts":3`} {
		if !strings.Contains(contents, expected) {
			t.Errorf("SSE missing %q: %s", expected, contents)
		}
	}
	if strings.Index(contents, replay.ID) > strings.Index(contents, live.ID) || service.historyID != instanceID || service.cursor != request.Header.Get("Last-Event-ID") {
		t.Errorf("SSE order/input = %s, %s, %q", contents, service.historyID, service.cursor)
	}
	if response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("SSE headers = %#v", response.Header)
	}
}

func TestEnergySSERouteValidatesCursorAndHonorsProtectedMiddleware(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	service := &energyHandlerService{err: energy.ErrInvalidLiveCursor}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	response := energyRequest(t, app, "/api/plugins/"+instanceID.String()+"/energy/stream")
	assertEnergyStatus(t, response, fiber.StatusBadRequest)
	_ = response.Body.Close()

	service = &energyHandlerService{}
	app = fiber.New()
	protected := app.Group("/api", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusUnauthorized) })
	RegisterRoutes(protected, NewHandler(service))
	response = energyRequest(t, app, "/api/plugins/"+instanceID.String()+"/energy/stream")
	assertEnergyStatus(t, response, fiber.StatusUnauthorized)
	_ = response.Body.Close()
	if service.historyID != uuid.Nil {
		t.Errorf("protected stream reached service for %s", service.historyID)
	}
}

type energyHandlerService struct {
	overview    *energy.OverviewResult
	history     *energy.HistoryResult
	export      string
	err         error
	validateErr error
	overviewID  uuid.UUID
	historyID   uuid.UUID
	validateID  uuid.UUID
	exportID    uuid.UUID
	input       energy.HistoryInput
	live        *energy.LiveSubscription
	cursor      string
	run         *energy.MeasurementRun
	resetInput  energy.ResetRunInput
}

func (service *energyHandlerService) Overview(_ context.Context, id uuid.UUID) (*energy.OverviewResult, error) {
	service.overviewID = id
	return service.overview, service.err
}

func (service *energyHandlerService) History(_ context.Context, id uuid.UUID, input energy.HistoryInput) (*energy.HistoryResult, error) {
	service.historyID, service.input = id, input
	return service.history, service.err
}

func (service *energyHandlerService) ValidateHistory(_ context.Context, id uuid.UUID, input energy.HistoryInput) error {
	service.validateID, service.input = id, input
	if service.validateErr != nil {
		return service.validateErr
	}
	return service.err
}

func (service *energyHandlerService) ExportCSV(_ context.Context, id uuid.UUID, input energy.HistoryInput, writer io.Writer) error {
	service.exportID, service.input = id, input
	if service.err != nil {
		return service.err
	}
	_, err := io.WriteString(writer, service.export)
	return err
}

func (service *energyHandlerService) SubscribeLive(_ context.Context, id uuid.UUID, cursor string) (*energy.LiveSubscription, error) {
	service.historyID, service.cursor = id, cursor
	return service.live, service.err
}

func (service *energyHandlerService) CurrentRun(_ context.Context, id uuid.UUID) (*energy.MeasurementRun, error) {
	service.historyID = id
	return service.run, service.err
}

func (service *energyHandlerService) ResetRun(_ context.Context, id uuid.UUID, input energy.ResetRunInput) (*energy.MeasurementRun, error) {
	service.historyID = id
	service.resetInput = input
	return service.run, service.err
}

func (service *energyHandlerService) ListArchivedRuns(_ context.Context, id uuid.UUID) ([]energy.MeasurementRun, error) {
	service.historyID = id
	return nil, service.err
}

func (service *energyHandlerService) ExportArchivedRunCSV(_ context.Context, id, _ uuid.UUID, writer io.Writer) error {
	service.exportID = id
	if service.err != nil {
		return service.err
	}
	_, err := io.WriteString(writer, service.export)
	return err
}

func energyRequest(t *testing.T, app *fiber.App, path string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	return response
}

func assertEnergyStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, want, body)
	}
}

func decodeEnergyResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

var _ Service = (*energyHandlerService)(nil)
