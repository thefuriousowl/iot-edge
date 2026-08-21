package handlers

import (
	"errors"
	"math"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/api/middleware"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/service"
)

const refreshTokenCookieName = "refresh_token"

type AuthHandler struct {
	auth     service.AuthService
	validate *validator.Validate
}

func NewAuthHandler(auth service.AuthService) *AuthHandler {
	return &AuthHandler{
		auth:     auth,
		validate: validator.New(),
	}
}

func (h *AuthHandler) SetupStatus(c *fiber.Ctx) error {
	required, err := h.auth.IsSetupRequired(c.UserContext())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Internal server error",
			},
		})
	}

	return c.Status(200).JSON(fiber.Map{
		"setup_required": required,
	})
}

func (h *AuthHandler) Setup(c *fiber.Ctx) error {
	var request domain.SetupRequest

	if err := c.BodyParser(&request); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Invalid request body",
			},
		})
	}

	if err := h.validate.Struct(request); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Invalid request data",
			},
		})
	}

	user, err := h.auth.Setup(
		c.UserContext(),
		request.Username,
		request.Password,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPasswordRequirements):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH006",
					"message": "Password requirements not met",
				},
			})

		case errors.Is(err, service.ErrSetupAlreadyCompleted):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH008",
					"message": "Setup already completed",
				},
			})

		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "INTERNAL_ERROR",
					"message": "Internal server error",
				},
			})
		}
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "Setup completed successfully",
		"user": fiber.Map{
			"id":         user.ID,
			"username":   user.Username,
			"created_at": user.CreatedAt,
		},
	})
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var request domain.LoginRequest

	if err := c.BodyParser(&request); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Invalid request body",
			},
		})
	}

	if err := h.validate.Struct(request); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "VALIDATION_ERROR",
				"message": "Invalid request data",
			},
		})
	}

	tokens, err := h.auth.Login(
		c.UserContext(),
		request.Username,
		request.Password,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredentials):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH001",
					"message": "Invalid credentials",
				},
			})

		case errors.Is(err, service.ErrAccountLocked):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH002",
					"message": "Account locked",
				},
			})

		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "INTERNAL_ERROR",
					"message": "Internal server error",
				},
			})
		}
	}

	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    tokens.RefreshToken,
		Path:     "/api/auth",
		Expires:  tokens.RefreshExpiresAt,
		Secure:   true,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
	})

	expiresIn := int64(math.Ceil(time.Until(tokens.AccessExpiresAt).Seconds()))
	if expiresIn < 0 {
		expiresIn = 0
	}

	return c.Status(fiber.StatusOK).JSON(domain.LoginResponse{
		AccessToken: tokens.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
	})
}

func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	refreshToken := c.Cookies(refreshTokenCookieName)

	if refreshToken == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "AUTH005",
				"message": "Session expired",
			},
		})
	}

	result, err := h.auth.Refresh(
		c.UserContext(),
		refreshToken,
	)

	if err != nil {
		switch {
		case errors.Is(err, service.ErrSessionExpired):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH005",
					"message": "Session expired",
				},
			})

		case errors.Is(err, service.ErrAccountLocked):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH002",
					"message": "Account locked",
				},
			})

		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "INTERNAL_ERROR",
					"message": "Internal server error",
				},
			})
		}
	}

	if result == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Internal server error",
			},
		})
	}

	expiresIn := int64(
		math.Ceil(
			time.Until(result.AccessExpiresAt).Seconds(),
		),
	)

	if expiresIn < 0 {
		expiresIn = 0
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"access_token": result.AccessToken,
		"expires_in":   expiresIn,
	})

}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	userID, ok := c.Locals(
		middleware.LocalUserID,
	).(uuid.UUID)
	if !ok {
		clearRefreshTokenCookie(c)

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "AUTH004",
				"message": "Invalid token",
			},
		})
	}

	refreshToken := c.Cookies(refreshTokenCookieName)
	if refreshToken == "" {
		clearRefreshTokenCookie(c)

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "AUTH005",
				"message": "Session expired",
			},
		})
	}

	err := h.auth.Logout(
		c.UserContext(),
		userID,
		refreshToken,
	)
	if err != nil {
		if errors.Is(err, service.ErrSessionExpired) {
			clearRefreshTokenCookie(c)

			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH005",
					"message": "Session expired",
				},
			})
		}

		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Internal server error",
			},
		})
	}

	clearRefreshTokenCookie(c)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "Logged out successfully",
	})
}
func clearRefreshTokenCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    "",
		Path:     "/api/auth",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		Secure:   true,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID, ok := c.Locals(
		middleware.LocalUserID,
	).(uuid.UUID)

	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "AUTH004",
				"message": "Invalid token",
			},
		})
	}

	user, err := h.auth.CurrentUser(
		c.UserContext(),
		userID,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrSessionExpired):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH005",
					"message": "Session expired",
				},
			})

		case errors.Is(err, service.ErrAccountLocked):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "AUTH002",
					"message": "Account locked",
				},
			})

		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "INTERNAL_ERROR",
					"message": "Internal server error",
				},
			})
		}
	}

	if user == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": "Internal server error",
			},
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"id":         user.ID,
		"username":   user.Username,
		"created_at": user.CreatedAt,
		"last_login": user.LastLogin,
	})
}
