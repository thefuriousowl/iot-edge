package dataloggerhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/data-loggers")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Get("/:id/history", handler.History)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
}
