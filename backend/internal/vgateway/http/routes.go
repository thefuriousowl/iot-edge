package vgatewayhttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/vgateways")

	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Post("/test", handler.TestConnectionConfig)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
	routes.Post("/:id/connect", handler.Connect)
	routes.Post("/:id/disconnect", handler.Disconnect)
	routes.Post("/:id/test", handler.TestConnection)
	routes.Get("/:id/status", handler.Status)
}
