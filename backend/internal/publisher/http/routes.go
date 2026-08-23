package publisherhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	router.Get("/publisher-types", handler.Types)
	router.Get("/publisher-sources", handler.Sources)
	router.Post("/publisher-payloads/validate", handler.ValidatePayload)
	routes := router.Group("/data-publishers")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Post("/:id/enable", handler.Enable)
	routes.Post("/:id/disable", handler.Disable)
	routes.Post("/:id/restart", handler.Restart)
	routes.Post("/:id/probe-listener", handler.ProbeListener)
	routes.Get("/:id/status", handler.Status)
	routes.Get("/:id/diagnostics", handler.Diagnostics)
	routes.Post("/:id/test-connection", handler.TestConnection)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
}
