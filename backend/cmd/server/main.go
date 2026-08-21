package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/thefuriousowl/iot-edge/internal/api/handlers"
	"github.com/thefuriousowl/iot-edge/internal/api/routes"
	"github.com/thefuriousowl/iot-edge/internal/config"
	"github.com/thefuriousowl/iot-edge/internal/repository"
	"github.com/thefuriousowl/iot-edge/internal/service"
)

func main() {
	// Load application configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	// Connect to PostgreSQL
	db, err := config.ConnectDatabase(cfg)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to access database connection: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			log.Printf("failed to close database connection: %v", err)
		}
	}()

	userRepo := repository.NewUserRepository(db)
	authService, err := service.NewAuthService(
		userRepo, cfg,
	)
	if err != nil {
		log.Fatalf("failed to initialize auth service: %v", err)
	}

	authHandler := handlers.NewAuthHandler(authService)

	app := newApp()

	routes.RegisterAuthRoutes(
		app.Group("/api"),
		authHandler,
		authService,
	)

	// Start HTTP server
	address := ":" + cfg.Port
	log.Printf("server listening on %s", address)

	if err := app.Listen(address); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

func newApp() *fiber.App {
	app := fiber.New()

	app.Use(logger.New())
	app.Use(cors.New())

	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status":  "ok",
			"version": "0.1.0",
		})
	})

	return app
}
