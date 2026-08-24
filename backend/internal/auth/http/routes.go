package authhttp

import (
	"github.com/gofiber/fiber/v2"
)

func RegisterAuthRoutes(
	router fiber.Router,
	handler *AuthHandler,
	tokenParser AccessTokenParser,
) {
	auth := router.Group("/auth")

	// READER ENDPOINT
	auth.Get("/setup/status", handler.SetupStatus)
	auth.Get(
		"/me",
		RequireAuth(tokenParser),
		handler.Me,
	)

	// WRITER ENDPOINT
	auth.Post("/setup", handler.Setup)
	auth.Post("/login", handler.Login)
	auth.Post("/refresh", handler.Refresh)
	auth.Post(
		"/logout",
		RequireAuth(tokenParser),
		handler.Logout,
	)
	auth.Post(
		"/change-password",
		RequireAuth(tokenParser),
		handler.ChangePassword,
	)

}
