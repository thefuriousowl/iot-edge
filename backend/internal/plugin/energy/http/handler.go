package energyhttp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/plugin/energy"
)

const (
	energyStreamRetryMilliseconds = 3000
	energyStreamHeartbeatInterval = 15 * time.Second
)

type Service interface {
	Overview(context.Context, uuid.UUID) (*energy.OverviewResult, error)
	History(context.Context, uuid.UUID, energy.HistoryInput) (*energy.HistoryResult, error)
	ValidateHistory(context.Context, uuid.UUID, energy.HistoryInput) error
	ExportCSV(context.Context, uuid.UUID, energy.HistoryInput, io.Writer) error
	SubscribeLive(context.Context, uuid.UUID, string) (*energy.LiveSubscription, error)
}

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (handler *Handler) Overview(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Energy Plugin instance ID")
	}
	result, err := handler.service.Overview(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (handler *Handler) History(c *fiber.Ctx) error {
	id, input, err := parseHistory(c)
	if err != nil {
		return validation(c, "Invalid Energy history query parameters")
	}
	result, err := handler.service.History(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{
		"instance_id": result.InstanceID, "logger_id": result.LoggerID, "timezone": result.Timezone,
		"bucket": result.Bucket, "currency": result.Currency, "tariff_mode": result.TariffMode, "tariff_tag_id": result.TariffTagID, "rate_per_kwh": result.RatePerKWh, "data": result.Data,
		"pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages},
	})
}

func (handler *Handler) ExportCSV(c *fiber.Ctx) error {
	id, input, err := parseHistory(c)
	if err != nil {
		return validation(c, "Invalid Energy export parameters")
	}
	ctx := c.UserContext()
	if err := handler.service.ValidateHistory(ctx, id, input); err != nil {
		return handleError(c, err)
	}
	reader, writer := io.Pipe()
	go func() {
		writer.CloseWithError(handler.service.ExportCSV(ctx, id, input, writer))
	}()
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="energy-`+id.String()+`.csv"`)
	return c.SendStream(reader)
}

func (handler *Handler) Stream(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Energy Plugin instance ID")
	}
	subscription, err := handler.service.SubscribeLive(c.UserContext(), id, c.Get("Last-Event-ID"))
	if err != nil {
		return handleError(c, err)
	}
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	c.Set(fiber.HeaderConnection, "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Context().SetBodyStreamWriter(func(writer *bufio.Writer) {
		defer subscription.Unsubscribe()
		if _, err := fmt.Fprintf(writer, "retry: %d\n: connected\n\n", energyStreamRetryMilliseconds); err != nil {
			return
		}
		if subscription.Reset {
			if _, err := writer.WriteString("event: energy_reset\ndata: {\"reason\":\"cursor_unavailable\"}\n\n"); err != nil {
				return
			}
		}
		for _, event := range subscription.Replay {
			if err := writeLiveEvent(writer, event); err != nil {
				return
			}
		}
		if err := writer.Flush(); err != nil {
			return
		}
		heartbeat := time.NewTicker(energyStreamHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case event, open := <-subscription.Stream:
				if !open {
					return
				}
				if err := writeLiveEvent(writer, event); err != nil {
					return
				}
			case <-heartbeat.C:
				if _, err := writer.WriteString(": keep-alive\n\n"); err != nil {
					return
				}
			}
			if err := writer.Flush(); err != nil {
				return
			}
		}
	})
	return nil
}

func writeLiveEvent(writer io.Writer, event energy.LiveEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %s\nevent: energy_metrics\ndata: %s\n\n", event.ID, payload)
	return err
}

func parseHistory(c *fiber.Ctx) (uuid.UUID, energy.HistoryInput, error) {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return uuid.Nil, energy.HistoryInput{}, err
	}
	input := energy.HistoryInput{Bucket: datalogger.QueryBucket(c.Query("bucket", string(datalogger.QueryBucket1Hour)))}
	for name, target := range map[string]*time.Time{"from": &input.From, "to": &input.To} {
		raw := c.Query(name)
		if raw == "" {
			return uuid.Nil, energy.HistoryInput{}, errors.New("query range is required")
		}
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return uuid.Nil, energy.HistoryInput{}, parseErr
		}
		*target = value.UTC()
	}
	input.Page, err = positiveQuery(c, "page", 1)
	if err == nil {
		input.PerPage, err = positiveQuery(c, "per_page", 100)
	}
	return id, input, err
}

func positiveQuery(c *fiber.Ctx, name string, fallback int) (int, error) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errors.New("query value must be positive")
	}
	return value, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid UUID")
	}
	return id, nil
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, plugin.ErrInstanceNotFound):
		return apiError(c, fiber.StatusNotFound, "ENG001", "Energy Plugin instance not found")
	case errors.Is(err, energy.ErrEnergyInstanceRequired):
		return apiError(c, fiber.StatusConflict, "ENG002", "Plugin instance is not Energy Management")
	case errors.Is(err, energy.ErrInvalidHistoryQuery):
		return validation(c, "Invalid Energy history query")
	case errors.Is(err, energy.ErrInvalidLiveCursor):
		return validation(c, "Invalid Last-Event-ID")
	case errors.Is(err, energy.ErrLiveHubRequired):
		return apiError(c, fiber.StatusServiceUnavailable, "ENG004", "Energy real-time monitoring is unavailable")
	case errors.Is(err, energy.ErrInvalidConfiguration):
		return apiError(c, fiber.StatusConflict, "ENG003", "Energy Plugin configuration is invalid")
	default:
		return apiError(c, fiber.StatusInternalServerError, "ENG500", "Energy data is temporarily unavailable")
	}
}

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "ENG000", message)
}

func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}
