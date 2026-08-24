package dataloggerhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	router.Get("/data-management/overview", handler.ManagementOverview)
	routes := router.Group("/data-loggers")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Get("/:id/history", handler.History)
	routes.Get("/:id/query", handler.Query)
	routes.Get("/:id/retention", handler.RetentionStatus)
	routes.Get("/:id/retention/preview", handler.RetentionPreview)
	routes.Post("/:id/retention/cleanup", handler.RetentionCleanup)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
}
