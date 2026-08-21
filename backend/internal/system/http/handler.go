package systemhttp

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/thefuriousowl/iot-edge/internal/system"
)

type ConnectivityChecker interface {
	Check(context.Context) system.InternetStatusResult
}

type Handler struct {
	connectivity ConnectivityChecker
}

func NewHandler(connectivity ConnectivityChecker) *Handler {
	return &Handler{connectivity: connectivity}
}

func (h *Handler) InternetStatus(c *fiber.Ctx) error {
	return c.Status(fiber.StatusOK).JSON(h.connectivity.Check(c.UserContext()))
}
