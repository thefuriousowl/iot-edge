package taghttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/tags")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Post("/preview", handler.Preview)
	routes.Post("/validate-expression", handler.ValidateExpression)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
	routes.Post("/:id/preview", handler.PreviewSaved)
}
