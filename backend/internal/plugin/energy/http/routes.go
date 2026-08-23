package energyhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/plugins/:id/energy")
	routes.Get("/overview", handler.Overview)
	routes.Get("/history", handler.History)
	routes.Get("/export.csv", handler.ExportCSV)
	routes.Get("/stream", handler.Stream)
}
