package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/thefuriousowl/iot-edge/internal/service"
)

const (
	LocalUserID   = "auth.user_id"
	LocalUsername = "auth.username"
	LocalClaims   = "auth.claims"
)

type AccessTokenParser interface {
	ParseToken(
		tokenString string,
		expectedType string,
	) (*service.TokenClaims, error)
}

func RequireAuth(auth AccessTokenParser) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authorization := c.Get(fiber.HeaderAuthorization)
		parts := strings.Fields(authorization)

		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return unauthorized(c)
		}

		claims, err := auth.ParseToken(parts[1], service.TokenTypeAccess)
		if err != nil || claims == nil {
			return unauthorized(c)
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			return unauthorized(c)
		}

		c.Locals(LocalUserID, userID)
		c.Locals(LocalUsername, claims.Username)
		c.Locals(LocalClaims, claims)

		return c.Next()
	}
}

func unauthorized(c *fiber.Ctx) error {
	c.Set(fiber.HeaderWWWAuthenticate, "Bearer")

	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    "AUTH004",
			"message": "Invalid token",
		},
	})
}
