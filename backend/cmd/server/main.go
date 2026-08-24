package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/thefuriousowl/iot-edge/internal/auth"
	authhttp "github.com/thefuriousowl/iot-edge/internal/auth/http"
	authpostgres "github.com/thefuriousowl/iot-edge/internal/auth/postgres"
	"github.com/thefuriousowl/iot-edge/internal/config"
	"github.com/thefuriousowl/iot-edge/internal/credential"
	credentialhttp "github.com/thefuriousowl/iot-edge/internal/credential/http"
	credentialpostgres "github.com/thefuriousowl/iot-edge/internal/credential/postgres"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	dataloggerhttp "github.com/thefuriousowl/iot-edge/internal/datalogger/http"
	dataloggerpostgres "github.com/thefuriousowl/iot-edge/internal/datalogger/postgres"
	"github.com/thefuriousowl/iot-edge/internal/datalogger/tagsnapshot"
	"github.com/thefuriousowl/iot-edge/internal/device"
	devicehttp "github.com/thefuriousowl/iot-edge/internal/device/http"
	devicepostgres "github.com/thefuriousowl/iot-edge/internal/device/postgres"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	pluginenergy "github.com/thefuriousowl/iot-edge/internal/plugin/energy"
	energyhttp "github.com/thefuriousowl/iot-edge/internal/plugin/energy/http"
	pluginhttp "github.com/thefuriousowl/iot-edge/internal/plugin/http"
	pluginpostgres "github.com/thefuriousowl/iot-edge/internal/plugin/postgres"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	publisherhttp "github.com/thefuriousowl/iot-edge/internal/publisher/http"
	publisherpostgres "github.com/thefuriousowl/iot-edge/internal/publisher/postgres"
	"github.com/thefuriousowl/iot-edge/internal/report"
	reporthttp "github.com/thefuriousowl/iot-edge/internal/report/http"
	reportpostgres "github.com/thefuriousowl/iot-edge/internal/report/postgres"
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
	dataLoggerBatchBroker, err := datalogger.NewCommittedBatchBroker()
	if err != nil {
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to initialize committed Data Logger batch broker: %v", err)
	}
	dataLoggerHistory := dataloggerpostgres.NewHistoryRepository(
		db,
		dataloggerpostgres.WithCommittedBatchPublisher(dataLoggerBatchBroker),
	)
	dataLoggerBatchFeed, err := datalogger.NewCommittedBatchFeed(dataLoggerHistory, dataLoggerBatchBroker)
	if err != nil {
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to initialize committed Data Logger batch feed: %v", err)
	}
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
	dataLoggerRetentionRuntime, err := datalogger.NewRetentionRuntime(dataLoggerRepository, dataLoggerService)
	if err != nil {
		dataLoggerRuntime.Stop()
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to initialize Data Logger retention runtime: %v", err)
	}
	if err := dataLoggerRetentionRuntime.Start(runtimeContext); err != nil {
		dataLoggerRuntime.Stop()
		acquisitionRuntime.Stop()
		tagValues.Stop()
		log.Fatalf("failed to start Data Logger retention runtime: %v", err)
	}
	energyLiveHub, err := pluginenergy.NewLiveHub()
	if err != nil {
		log.Fatalf("failed to initialize Energy live hub: %v", err)
	}
	energyDefinition, err := pluginenergy.NewDefinition(dataLoggerRepository, pluginenergy.WithRuntimeFactory(energyLiveHub))
	if err != nil {
		log.Fatalf("failed to initialize Energy Plugin definition: %v", err)
	}
	pluginRegistry, err := plugin.NewRegistry(energyDefinition)
	if err != nil {
		log.Fatalf("failed to initialize Plugin registry: %v", err)
	}
	pluginRepository := pluginpostgres.NewRepository(db)
	pluginService, err := plugin.NewService(pluginRepository, pluginRegistry)
	if err != nil {
		log.Fatalf("failed to initialize Plugin service: %v", err)
	}
	pluginOutputBroker, err := plugin.NewOutputBroker()
	if err != nil {
		log.Fatalf("failed to initialize Plugin output broker: %v", err)
	}
	pluginOutputStore, err := plugin.NewOutputStore(pluginpostgres.NewOutputRepository(db), pluginService, pluginOutputBroker)
	if err != nil {
		log.Fatalf("failed to initialize Plugin output store: %v", err)
	}
	pluginHost, err := plugin.NewCapabilityHost(map[plugin.Capability]any{
		plugin.CapabilityLoggerCommittedBatches: dataLoggerBatchFeed,
		plugin.CapabilityLoggerHistoryBatches:   dataLoggerHistory,
		plugin.CapabilityPluginOutputsPublish:   pluginOutputStore,
	})
	if err != nil {
		log.Fatalf("failed to initialize Plugin capability host: %v", err)
	}
	pluginManager, err := plugin.NewManager(pluginRepository, pluginRegistry, pluginHost)
	if err != nil {
		log.Fatalf("failed to initialize Plugin manager: %v", err)
	}
	if err := pluginManager.Start(runtimeContext); err != nil {
		log.Fatalf("failed to start Plugin manager: %v", err)
	}
	publisherSources, err := publisher.NewSourceFeed(tagService, tagValues, pluginService, pluginOutputStore)
	if err != nil {
		log.Fatalf("failed to initialize Data Publisher sources: %v", err)
	}
	publisherDefinitions, err := publisher.NewDefaultDefinitionRegistry()
	if err != nil {
		log.Fatalf("failed to initialize Data Publisher definitions: %v", err)
	}
	publisherRepository := publisherpostgres.NewRepository(db)
	publisherPayloadEngine := publisher.NewJSONPayloadEngine()
	publisherHTTPProber, err := publisher.NewHTTPServerProber()
	if err != nil {
		log.Fatalf("failed to initialize HTTP Publisher listener probe: %v", err)
	}
	var publisherManager *publisher.Manager
	var publisherRuntimeErrors <-chan error
	publisherHandlerOptions := []publisherhttp.HandlerOption{publisherhttp.WithHTTPServerListenerProber(publisherHTTPProber)}
	publisherServiceOptions := []publisher.ServiceOption{}
	credentialHandler := credentialhttp.NewHandler(nil)
	if len(cfg.PublisherMasterKey) != 0 {
		publisherCipher, err := publisher.NewAESGCMSecretCipher(cfg.PublisherMasterKeyID, cfg.PublisherMasterKey)
		for index := range cfg.PublisherMasterKey {
			cfg.PublisherMasterKey[index] = 0
		}
		cfg.PublisherMasterKey = nil
		if err != nil {
			log.Fatalf("failed to initialize Data Publisher secret cipher: %v", err)
		}
		publisherSecretRepository := publisherpostgres.NewSecretRepository(db)
		publisherSecretRotations, err := publisher.NewSecretRotationBroker(64)
		if err != nil {
			log.Fatalf("failed to initialize Data Publisher secret rotations: %v", err)
		}
		publisherSecretService, err := publisher.NewSecretService(publisherSecretRepository, publisherCipher, publisherSecretRotations)
		if err != nil {
			log.Fatalf("failed to initialize Data Publisher secret service: %v", err)
		}
		publisherSecretVault, err := publisher.NewSecretVault(publisherSecretRepository, publisherCipher)
		if err != nil {
			log.Fatalf("failed to initialize Data Publisher secret vault: %v", err)
		}
		credentialRepository := credentialpostgres.NewRepository(db)
		credentialService, err := credential.NewService(credentialRepository, publisherSecretService)
		if err != nil {
			log.Fatalf("failed to initialize Credential service: %v", err)
		}
		credentialHandler = credentialhttp.NewHandler(credentialService)
		publisherServiceOptions = append(publisherServiceOptions, publisher.WithCredentialValidator(credentialService))
		publisherSecretResolver, err := credential.NewPublisherSecretResolver(publisherRepository, publisherSecretVault)
		if err != nil {
			log.Fatalf("failed to initialize Publisher Credential resolver: %v", err)
		}
		publisherTransports := publisher.NewTransportRegistry()
		httpPublisherFactory, err := publisher.NewHTTPTransportFactory(publisherSecretResolver, publisherPayloadEngine)
		if err != nil {
			log.Fatalf("failed to initialize HTTP Publisher transport: %v", err)
		}
		if err := publisherTransports.Register(publisher.TypeHTTPServer, httpPublisherFactory); err != nil {
			log.Fatalf("failed to register HTTP Publisher transport: %v", err)
		}
		mqttPublisherFactory, err := publisher.NewMQTTTransportFactory(publisherSecretResolver, publisherPayloadEngine)
		if err != nil {
			log.Fatalf("failed to initialize MQTT Publisher transport: %v", err)
		}
		if err := publisherTransports.Register(publisher.TypeMQTT, mqttPublisherFactory); err != nil {
			log.Fatalf("failed to register MQTT Publisher transport: %v", err)
		}
		publisherManager, err = publisher.NewManager(
			publisherRepository,
			publisherSources,
			publisherDefinitions,
			publisherTransports,
		)
		if err != nil {
			log.Fatalf("failed to initialize Data Publisher manager: %v", err)
		}
		if err := publisherManager.Start(runtimeContext); err != nil {
			log.Fatalf("failed to start Data Publisher manager: %v", err)
		}
		publisherConnectionTester, err := publisher.NewMQTTConnectionTester(publisherSecretResolver, publisherPayloadEngine)
		if err != nil {
			log.Fatalf("failed to initialize MQTT connection tester: %v", err)
		}
		publisherHandlerOptions = append(
			publisherHandlerOptions,
			publisherhttp.WithRuntimeManager(publisherManager),
			publisherhttp.WithMQTTConnectionTester(publisherConnectionTester),
		)
		publisherRuntimeErrors = publisherManager.Errors()
	} else {
		log.Printf("Data Publisher secrets and runtime are disabled: configure PUBLISHER_MASTER_KEY_ID and PUBLISHER_MASTER_KEY_BASE64")
	}
	publisherService, err := publisher.NewService(publisherRepository, publisherSources, publisherDefinitions, publisherServiceOptions...)
	if err != nil {
		log.Fatalf("failed to initialize Data Publisher service: %v", err)
	}
	publisherHandler := publisherhttp.NewHandler(publisherService, publisherSources, publisherPayloadEngine, publisherHandlerOptions...)
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if publisherManager != nil {
			if err := publisherManager.Stop(shutdownContext); err != nil {
				log.Printf("failed to stop Data Publisher manager: %v", err)
			}
		}
		if err := pluginManager.Stop(shutdownContext); err != nil {
			log.Printf("failed to stop Plugin manager: %v", err)
		}
		dataLoggerRetentionRuntime.Stop()
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
			case retentionError := <-dataLoggerRetentionRuntime.Errors():
				log.Printf("Data Logger retention runtime: %v", retentionError)
			case pluginError := <-pluginManager.Errors():
				log.Printf("Plugin runtime: %v", pluginError)
			case publisherError := <-publisherRuntimeErrors:
				log.Printf("Data Publisher runtime: %v", publisherError)
			}
		}
	}()
	dataLoggerHandler := dataloggerhttp.NewHandler(dataLoggerService)
	pluginHandler := pluginhttp.NewHandler(pluginService, pluginManager)
	energyService, err := pluginenergy.NewService(pluginRepository, dataLoggerHistory, pluginenergy.WithLiveHub(energyLiveHub))
	if err != nil {
		log.Fatalf("failed to initialize Energy service: %v", err)
	}
	energyHandler := energyhttp.NewHandler(energyService)
	reportService, err := report.NewService(reportpostgres.NewRepository(db), dataLoggerRepository, dataLoggerHistory)
	if err != nil {
		log.Fatalf("failed to initialize Report service: %v", err)
	}
	reportHandler := reporthttp.NewHandler(reportService)
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
	pluginhttp.RegisterRoutes(protectedAPI, pluginHandler)
	energyhttp.RegisterRoutes(protectedAPI, energyHandler)
	reporthttp.RegisterRoutes(protectedAPI, reportHandler)
	credentialhttp.RegisterRoutes(protectedAPI, credentialHandler)
	publisherhttp.RegisterRoutes(protectedAPI, publisherHandler)

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
