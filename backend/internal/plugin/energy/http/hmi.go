package energyhttp

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin/energy"
)

const HMIAPIKeyHeader = "X-IoT-Edge-HMI-Key"

type HMIConfig struct {
	PluginInstanceID uuid.UUID
	APIKey           string
}

type hmiMetric struct {
	Value       *float64   `json:"value"`
	Available   bool       `json:"available"`
	Reason      string     `json:"reason,omitempty"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
}

type hmiSnapshot struct {
	SchemaVersion         int                    `json:"schema_version"`
	GeneratedAt           time.Time              `json:"generated_at"`
	AsOf                  time.Time              `json:"as_of"`
	Currency              string                 `json:"currency"`
	RatePerKWh            float64                `json:"rate_per_kwh"`
	Run                   *energy.MeasurementRun `json:"run,omitempty"`
	MonthEstimatedCost    hmiMetric              `json:"month_covered_estimated_cost"`
	TodayEstimatedCost    hmiMetric              `json:"today_covered_estimated_cost"`
	InstantaneousCOP      hmiMetric              `json:"instantaneous_cop"`
	MonthElectricalEnergy hmiMetric              `json:"month_electrical_energy"`
	MonthThermalEnergy    hmiMetric              `json:"month_thermal_energy"`
}

func RegisterHMIRoutes(router fiber.Router, handler *Handler, cfg HMIConfig) error {
	if handler == nil || handler.service == nil || cfg.PluginInstanceID == uuid.Nil || len(cfg.APIKey) < 32 {
		return errors.New("invalid HMI Energy API configuration")
	}
	routes := router.Group("/hmi/v1/energy", requireHMIAPIKey(cfg.APIKey))
	routes.Get("/snapshot", handler.hmiSnapshot(cfg.PluginInstanceID))
	routes.Get("/run", handler.hmiCurrentRun(cfg.PluginInstanceID))
	routes.Post("/reset", handler.hmiResetRun(cfg.PluginInstanceID))
	return nil
}

func requireHMIAPIKey(expected string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		values := c.Context().Request.Header.PeekAll(HMIAPIKeyHeader)
		if len(values) != 1 || subtle.ConstantTimeCompare(values[0], []byte(expected)) != 1 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"code": "HMI001", "message": "HMI authentication required"})
		}
		return c.Next()
	}
}

func (handler *Handler) hmiSnapshot(instanceID uuid.UUID) fiber.Handler {
	return func(c *fiber.Ctx) error {
		overview, err := handler.service.Overview(c.UserContext(), instanceID)
		if err != nil {
			return handleError(c, err)
		}
		response := hmiSnapshot{SchemaVersion: 1, GeneratedAt: time.Now().UTC(), AsOf: overview.AsOf, Currency: overview.Currency, RatePerKWh: overview.RatePerKWh, Run: overview.Run}
		response.MonthEstimatedCost = ratioHMIMetric(overview.Month.Cost, overview.Month.From, overview.Month.To)
		response.TodayEstimatedCost = ratioHMIMetric(overview.Today.Cost, overview.Today.From, overview.Today.To)
		response.MonthElectricalEnergy = energyHMIMetric(overview.Month.Electrical, overview.Month.From, overview.Month.To)
		response.MonthThermalEnergy = energyHMIMetric(overview.Month.Thermal, overview.Month.From, overview.Month.To)
		if overview.Latest == nil {
			response.InstantaneousCOP = unavailableHMIMetric("waiting_for_committed_batch")
		} else {
			response.InstantaneousCOP = ratioHMIMetric(overview.Latest.COP, time.Time{}, time.Time{})
		}
		return c.JSON(response)
	}
}

func (handler *Handler) hmiCurrentRun(instanceID uuid.UUID) fiber.Handler {
	return func(c *fiber.Ctx) error {
		run, err := handler.service.CurrentRun(c.UserContext(), instanceID)
		if err != nil {
			return handleError(c, err)
		}
		return c.JSON(run)
	}
}

func (handler *Handler) hmiResetRun(instanceID uuid.UUID) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var request resetRunRequest
		if err := decodeStrictJSON(c.Body(), &request); err != nil {
			return validation(c, "Invalid Energy reset request")
		}
		run, err := handler.service.ResetRun(c.UserContext(), instanceID, energy.ResetRunInput{ExpectedRunID: request.ExpectedRunID, Name: request.Name, Reason: request.Reason})
		if err != nil {
			return handleError(c, err)
		}
		return c.JSON(run)
	}
}

func decodeStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func ratioHMIMetric(metric energy.RatioMetric, from, to time.Time) hmiMetric {
	if !metric.Valid {
		reason := metric.Error
		if reason == "" {
			reason = "unavailable"
		}
		return unavailableHMIMetric(reason)
	}
	value := metric.Value
	result := hmiMetric{Value: &value, Available: true}
	if !from.IsZero() {
		result.PeriodStart, result.PeriodEnd = &from, &to
	}
	return result
}

func energyHMIMetric(metric energy.EnergySummary, from, to time.Time) hmiMetric {
	if metric.CoveredSeconds <= 0 {
		return unavailableHMIMetric("insufficient_coverage")
	}
	value := metric.KilowattHours
	return hmiMetric{Value: &value, Available: true, PeriodStart: &from, PeriodEnd: &to}
}

func unavailableHMIMetric(reason string) hmiMetric {
	return hmiMetric{Available: false, Reason: reason}
}
