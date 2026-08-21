package routes

import (
	"github.com/gofiber/fiber/v2"

	"github.com/thefuriousowl/iot-edge/internal/api/handlers"
	"github.com/thefuriousowl/iot-edge/internal/api/middleware"
)

func RegisterAuthRoutes(
	router fiber.Router,
	handler *handlers.AuthHandler,
	tokenParser middleware.AccessTokenParser,
) {
	auth := router.Group("/auth")

	// READER ENDPOINT
	auth.Get("/setup/status", handler.SetupStatus)
	auth.Get(
		"/me",
		middleware.RequireAuth(tokenParser),
		handler.Me,
	)

	// WRITER ENDPOINT
	auth.Post("/setup", handler.Setup)
	auth.Post("/login", handler.Login)
	auth.Post("/refresh", handler.Refresh)
	auth.Post(
		"/logout",
		middleware.RequireAuth(tokenParser),
		handler.Logout,
	)

}
