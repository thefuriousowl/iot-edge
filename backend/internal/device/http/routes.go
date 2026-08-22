package devicehttp

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, handler *Handler) {
	gateways := router.Group("/vgateways/:vgateway_id/devices")
	gateways.Get("/", handler.ListDevices)
	gateways.Post("/", handler.CreateDevice)

	devices := router.Group("/devices")
	devices.Get("/", handler.ListDeviceInventory)
	devices.Get("/:id", handler.GetDevice)
	devices.Put("/:id", handler.UpdateDevice)
	devices.Delete("/:id", handler.DeleteDevice)
	devices.Get("/:device_id/datasources", handler.ListDatasources)
	devices.Post("/:device_id/datasources", handler.CreateDatasource)
	devices.Post("/:device_id/datasources/preview", handler.PreviewDatasource)

	datasources := router.Group("/datasources")
	datasources.Get("/:id", handler.GetDatasource)
	datasources.Put("/:id", handler.UpdateDatasource)
	datasources.Delete("/:id", handler.DeleteDatasource)
	datasources.Post("/:id/preview", handler.PreviewSavedDatasource)
	datasources.Get("/:id/raw", handler.RawDatasource)
	datasources.Get("/:id/stream", handler.StreamDatasource)
}
