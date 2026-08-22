package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/thefuriousowl/iot-edge/internal/auth"
	authhttp "github.com/thefuriousowl/iot-edge/internal/auth/http"
	authpostgres "github.com/thefuriousowl/iot-edge/internal/auth/postgres"
	"github.com/thefuriousowl/iot-edge/internal/config"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	dataloggerhttp "github.com/thefuriousowl/iot-edge/internal/datalogger/http"
	dataloggerpostgres "github.com/thefuriousowl/iot-edge/internal/datalogger/postgres"
	"github.com/thefuriousowl/iot-edge/internal/datalogger/tagsnapshot"
	"github.com/thefuriousowl/iot-edge/internal/device"
	devicehttp "github.com/thefuriousowl/iot-edge/internal/device/http"
	devicepostgres "github.com/thefuriousowl/iot-edge/internal/device/postgres"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	"github.com/thefuriousowl/iot-edge/internal/system"
	systemhttp "github.com/thefuriousowl/iot-edge/internal/system/http"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	taghttp "github.com/thefuriousowl/iot-edge/internal/tag/http"
	tagpostgres "github.com/thefuriousowl/iot-edge/internal/tag/postgres"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
	vgatewayhttp "github.com/thefuriousowl/iot-edge/internal/vgateway/http"
	vgatewaypostgres "github.com/thefuriousowl/iot-edge/internal/vgateway/postgres"
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

	userRepo := authpostgres.NewUserRepository(db)
	authService, err := auth.NewAuthService(
		userRepo,
		&auth.ServiceConfig{
			JWTSecret:        cfg.JWTSecret,
			JWTAccessExpiry:  cfg.JWTAccessExpiry,
			JWTRefreshExpiry: cfg.JWTRefreshExpiry,
		},
	)
	if err != nil {
		log.Fatalf("failed to initialize auth service: %v", err)
	}

	authHandler := authhttp.NewAuthHandler(
		authService,
		authhttp.WithSecureCookies(cfg.CookieSecure),
	)
	modbusDriver := modbus.NewDefaultModbusTCPDriver()
	vgatewayService, err := vgateway.NewVGatewayService(
		vgatewaypostgres.NewVGatewayRepository(db),
		vgateway.GatewayDriverRegistry{
			vgateway.VGatewayTypeModbusTCP: modbusDriver,
		},
	)
	if err != nil {
		log.Fatalf("failed to initialize vGateway service: %v", err)
	}
	deviceRepository := devicepostgres.NewRepository(db)
	vgatewayHandler := vgatewayhttp.NewHandler(
		vgatewayService,
		vgatewayhttp.WithDeviceCounter(deviceRepository),
	)
	deviceService, err := device.NewServiceWithGatewayRequestRecorder(
		deviceRepository,
		vgatewayService,
		modbusDriver,
	)
	if err != nil {
		log.Fatalf("failed to initialize device service: %v", err)
	}
	deviceHandler := devicehttp.NewHandler(deviceService)
	tagRepository := tagpostgres.NewRepository(db)
	tagService, err := tag.NewService(
		tagRepository,
		deviceService,
		tag.NewBinaryNumericDecoder(),
	)
	if err != nil {
		log.Fatalf("failed to initialize tag service: %v", err)
	}
	tagValues, err := tag.NewPersistentValueStore(tagpostgres.NewLatestValueRepository(db))
	if err != nil {
		log.Fatalf("failed to initialize persistent Tag value store: %v", err)
	}
	runtimeContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	if err := tagValues.Start(runtimeContext); err != nil {
		log.Fatalf("failed to hydrate persistent Tag values: %v", err)
	}
	acquisitionRuntime, err := tag.NewAcquisitionRuntime(tagRepository, tagService, deviceService, tagValues)
	if err != nil {
		tagValues.Stop()
		log.Fatalf("failed to initialize acquisition runtime: %v", err)
	}
	if err := acquisitionRuntime.Start(runtimeContext); err != nil {
		tagValues.Stop()
		log.Fatalf("failed to start acquisition runtime: %v", err)
	}
	tagHandler := taghttp.NewHandler(tagService, taghttp.WithValueMonitor(tagValues))
	dataLoggerRepository := dataloggerpostgres.NewRepository(db)
	dataLoggerHistory := dataloggerpostgres.NewHistoryRepository(db)
	dataLoggerService, err := datalogger.NewService(dataLoggerRepository, dataLoggerHistory)
	if err != nil {
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to initialize Data Logger service: %v", err)
	}
	dataLoggerSnapshots, err := tagsnapshot.NewReader(tagValues)
	if err != nil {
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to initialize Data Logger snapshots: %v", err)
	}
	dataLoggerRuntime, err := datalogger.NewRuntime(dataLoggerRepository, dataLoggerSnapshots, dataLoggerHistory)
	if err != nil {
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to initialize Data Logger runtime: %v", err)
	}
	if err := dataLoggerRuntime.Start(runtimeContext); err != nil {
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to start Data Logger runtime: %v", err)
	}
	defer func() {
		dataLoggerRuntime.Stop()
		acquisitionRuntime.Stop()
		tagValues.Stop()
	}()
	go func() {
		for {
			select {
			case <-runtimeContext.Done():
				return
			case runtimeError := <-acquisitionRuntime.Errors():
				log.Printf("acquisition runtime: %v", runtimeError)
			case persistenceError := <-tagValues.Errors():
				log.Printf("Tag value persistence: %v", persistenceError)
			case loggerError := <-dataLoggerRuntime.Errors():
				log.Printf("Data Logger runtime: %v", loggerError)
			}
		}
	}()
	dataLoggerHandler := dataloggerhttp.NewHandler(dataLoggerService)
	connectivityChecker, err := system.NewConnectivityChecker(
		cfg.InternetCheckAddress,
		cfg.InternetCheckTimeout,
	)
	if err != nil {
		log.Fatalf("failed to initialize internet connectivity checker: %v", err)
	}
	systemHandler := systemhttp.NewHandler(connectivityChecker)

	app := newApp(cfg.CORSAllowOrigins)

	authhttp.RegisterAuthRoutes(
		app.Group("/api"),
		authHandler,
		authService,
	)
	protectedAPI := app.Group("/api", authhttp.RequireAuth(authService))
	systemhttp.RegisterRoutes(protectedAPI, systemHandler)
	vgatewayhttp.RegisterRoutes(protectedAPI, vgatewayHandler)
	devicehttp.RegisterRoutes(protectedAPI, deviceHandler)
	taghttp.RegisterRoutes(protectedAPI, tagHandler)
	dataloggerhttp.RegisterRoutes(protectedAPI, dataLoggerHandler)

	// Start HTTP server
	address := ":" + cfg.Port
	log.Printf("server listening on %s", address)
	go func() {
		<-runtimeContext.Done()
		if err := app.Shutdown(); err != nil {
			log.Printf("failed to shut down server: %v", err)
		}
	}()

	if err := app.Listen(address); err != nil {
		log.Printf("failed to start server: %v", err)
	}
	stopSignal()
}

func newApp(corsAllowOrigins string) *fiber.App {
	app := fiber.New()

	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     corsAllowOrigins,
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, Last-Event-ID",
		AllowCredentials: true,
	}))

	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status":  "ok",
			"version": "0.1.0",
		})
	})

	return app
}
