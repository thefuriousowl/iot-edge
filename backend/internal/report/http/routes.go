package reporthttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/reports")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Get("/:id/query", handler.Query)
	routes.Get("/:id/export.csv", handler.ExportCSV)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
}
