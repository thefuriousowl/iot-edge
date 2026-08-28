package assethttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	routes := router.Group("/assets")
	routes.Get("/", handler.List)
	routes.Post("/", handler.Create)
	routes.Get("/roots", handler.Roots)
	routes.Get("/:id", handler.Get)
	routes.Put("/:id", handler.Update)
	routes.Delete("/:id", handler.Delete)
	routes.Post("/:id/move", handler.Move)
	routes.Get("/:id/children", handler.Children)
	routes.Get("/:id/tree", handler.Subtree)
	routes.Get("/:id/ancestors", handler.Ancestors)
	routes.Get("/:id/bindings", handler.Bindings)
	routes.Put("/:id/bindings", handler.ReplaceBindings)
	routes.Get("/:id/measurements", handler.Measurements)
	routes.Get("/:id/measurements/stream", handler.StreamMeasurements)
	routes.Get("/:id/connectivity", handler.AssetConnectivity)
	router.Get("/tags/:id/assets", handler.TagAssets)
}
