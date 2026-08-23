package credentialhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/credentials")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Put("/:id/secrets/:slot", handler.PutSecret)
	routes.Delete("/:id/secrets/:slot", handler.DeleteSecret)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
}
