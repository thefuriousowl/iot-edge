package systemhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/system")
	routes.Get("/internet-status", handler.InternetStatus)
}
