package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

var (
	ErrVGatewayRepositoryRequired = errors.New(
		"vGateway repository is required",
	)
	ErrVGatewayDriversRequired = errors.New(
		"vGateway drivers are required",
	)
	ErrVGatewayDriverRequired = errors.New(
		"vGateway driver is required",
	)
	ErrInvalidVGatewayConfig = errors.New(
		"invalid vGateway config",
	)
	ErrInvalidVGatewayName     = errors.New("invalid vGateway name")
	ErrUnsupportedVGatewayType = errors.New("unsupported vGateway type")
	ErrVGatewayDisabled        = errors.New("vGateway is disabled")
	ErrVGatewayNameExists      = errors.New("duplicated vGateway name")
	ErrVGatewayNotFound        = errors.New("vGateway not found")
)

const (
	DefaultVGatewayPage    = 1
	DefaultVGatewayPerPage = 20
	MaxVGatewayPerPage     = 100
)

type GatewayDriverRegistry map[domain.VGatewayType]protocol.GatewayDriver

type VGatewayView struct {
	domain.VGateway
	Status domain.VGatewayConnectionStatus `json:"status"`
}

type VGatewayListInput struct {
	Type    *domain.VGatewayType
	Enabled *bool
	Page    int
	PerPage int
}

type VGatewayListResult struct {
	Data       []VGatewayView
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type CreateVGatewayInput struct {
	Name        string
	Type        domain.VGatewayType
	Description *string
	Enabled     *bool
	Config      domain.VGatewayConfig
}

type OptionalDescription struct {
	Set   bool
	Value *string
}

type UpdateVGatewayInput struct {
	Name        *string
	Description OptionalDescription
	Enabled     *bool
	Config      *domain.VGatewayConfig
}

type vGatewayService struct {
	gateways repository.VGatewayRepository
	drivers  GatewayDriverRegistry

	mu       sync.RWMutex
	runtimes map[uuid.UUID]*vGatewayRuntime
	now      func() time.Time
}

func NewVGatewayService(
	gateways repository.VGatewayRepository,
	drivers GatewayDriverRegistry,
) (*vGatewayService, error) {
	if gateways == nil {
		return nil, ErrVGatewayRepositoryRequired
	}
	if len(drivers) == 0 {
		return nil, ErrVGatewayDriversRequired
	}

	ownedDrivers := make(GatewayDriverRegistry, len(drivers))
	for gatewayType, driver := range drivers {
		if gatewayType == "" || driver == nil {
			return nil, fmt.Errorf(
				"%w: %q",
				ErrVGatewayDriverRequired,
				gatewayType,
			)
		}
		ownedDrivers[gatewayType] = driver
	}

	return &vGatewayService{
		gateways: gateways,
		drivers:  ownedDrivers,
		runtimes: make(map[uuid.UUID]*vGatewayRuntime),
		now:      time.Now,
	}, nil
}

func (s *vGatewayService) driverFor(
	gatewayType domain.VGatewayType,
) (protocol.GatewayDriver, error) {
	driver, ok := s.drivers[gatewayType]
	if !ok {
		return nil, fmt.Errorf(
			"%w: %q",
			ErrUnsupportedVGatewayType,
			gatewayType,
		)
	}
	return driver, nil
}
