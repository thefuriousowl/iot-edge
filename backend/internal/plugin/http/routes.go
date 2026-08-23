package pluginhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	router.Get("/plugin-types", handler.Types)
	routes := router.Group("/plugins")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Post("/:id/enable", handler.Enable)
	routes.Post("/:id/disable", handler.Disable)
	routes.Post("/:id/restart", handler.Restart)
	routes.Get("/:id/status", handler.Status)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
}
