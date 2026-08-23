package publisher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrTransportRequired      = errors.New("Data Publisher transport is required")
	ErrTransportFactoryExists = errors.New("Data Publisher transport factory is already registered")
	ErrTransportUnavailable   = errors.New("Data Publisher transport is unavailable")
	ErrInvalidListenerClaim   = errors.New("invalid Data Publisher listener claim")
	ErrListenerConflict       = errors.New("Data Publisher listener conflicts with another Publisher")
)

type ListenerClaim struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

type Transport interface {
	Publish(context.Context, SourceSnapshot) error
	Close(context.Context) error
}

type TransportMetrics struct {
	ReconnectCount        uint64
	Connected             bool
	ConnectionCount       uint64
	DeliveryCount         uint64
	DeliveryFailureCount  uint64
	TransportQueueDepth   uint64
	TransportDropCount    uint64
	DiagnosticCount       uint64
	DiagnosticDropCount   uint64
	ExternalRequestCount  uint64
	RejectedRequestCount  uint64
	ActiveConnections     uint64
	LastExternalRequestAt *time.Time
	LastConnectedAt       *time.Time
	LastDeliveredAt       *time.Time
	LastDiagnosticAt      *time.Time
	TransportError        string
}

type TransportMetricsProvider interface {
	Metrics() TransportMetrics
}

type TransportFailureSource interface {
	Failures() <-chan error
}

type TransportFactory interface {
	NewTransport(context.Context, Publisher) (Transport, error)
	ListenerClaims(Publisher) ([]ListenerClaim, error)
}

type ResolvedTransportFactory interface {
	NewResolvedTransport(context.Context, Publisher, []ResolvedSource) (Transport, error)
}

type TransportRegistry struct {
	mu        sync.RWMutex
	factories map[Type]TransportFactory
}

func NewTransportRegistry() *TransportRegistry {
	return &TransportRegistry{factories: make(map[Type]TransportFactory)}
}

func (registry *TransportRegistry) Register(publisherType Type, factory TransportFactory) error {
	if registry == nil || !validPublisherType(publisherType) || isNilSourceDependency(factory) {
		return ErrTransportRequired
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.factories == nil {
		registry.factories = make(map[Type]TransportFactory)
	}
	if _, exists := registry.factories[publisherType]; exists {
		return fmt.Errorf("%w: %s", ErrTransportFactoryExists, publisherType)
	}
	registry.factories[publisherType] = factory
	return nil
}

func (registry *TransportRegistry) Find(publisherType Type) (TransportFactory, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: %s", ErrTransportUnavailable, publisherType)
	}
	registry.mu.RLock()
	factory, exists := registry.factories[publisherType]
	registry.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrTransportUnavailable, publisherType)
	}
	return factory, nil
}

func normalizeListenerClaims(publisherType Type, claims []ListenerClaim) ([]ListenerClaim, error) {
	if len(claims) == 0 {
		return []ListenerClaim{}, nil
	}
	if publisherType != TypeHTTPServer && publisherType != TypeModbusTCPServer {
		return nil, ErrInvalidListenerClaim
	}
	normalized := make([]ListenerClaim, 0, len(claims))
	for _, claim := range claims {
		network := strings.ToLower(strings.TrimSpace(claim.Network))
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, ErrInvalidListenerClaim
		}
		host, portText, err := net.SplitHostPort(strings.TrimSpace(claim.Address))
		if err != nil {
			return nil, ErrInvalidListenerClaim
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, ErrInvalidListenerClaim
		}
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "*"
		}
		candidate := ListenerClaim{Network: network, Address: net.JoinHostPort(host, strconv.Itoa(port))}
		for _, existing := range normalized {
			if listenerClaimsConflict(existing, candidate) {
				return nil, ErrInvalidListenerClaim
			}
		}
		normalized = append(normalized, candidate)
	}
	return normalized, nil
}

func listenerClaimsConflict(first, second ListenerClaim) bool {
	if !listenerNetworksOverlap(first.Network, second.Network) {
		return false
	}
	firstHost, firstPort, firstErr := net.SplitHostPort(first.Address)
	secondHost, secondPort, secondErr := net.SplitHostPort(second.Address)
	if firstErr != nil || secondErr != nil || firstPort != secondPort {
		return false
	}
	return firstHost == "*" || secondHost == "*" || firstHost == secondHost
}

func listenerNetworksOverlap(first, second string) bool {
	return first == second || first == "tcp" || second == "tcp"
}
