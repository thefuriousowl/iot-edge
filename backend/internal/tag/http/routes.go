package taghttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	router.Get("/sse/tags", handler.StreamAllValues)
	routes := router.Group("/tags")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Post("/preview", handler.Preview)
	routes.Post("/validate-expression", handler.ValidateExpression)
	routes.Get("/:id", handler.Get)
	routes.Get("/:id/values", handler.Values)
	routes.Get("/:id/stream", handler.StreamValues)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
	routes.Post("/:id/preview", handler.PreviewSaved)
}
