package publisher

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMQTTConnectionTesterRequired = errors.New("MQTT connection tester dependencies are required")
	ErrMQTTConnectionTestFailed     = errors.New("MQTT connection test failed")
	ErrMQTTConnectionDNSFailed      = errors.New("MQTT broker hostname could not be resolved")
	ErrMQTTConnectionTCPFailed      = errors.New("MQTT broker TCP connection failed")
	ErrMQTTConnectionTLSFailed      = errors.New("MQTT TLS verification or handshake failed")
	ErrMQTTConnectionAuthFailed     = errors.New("MQTT authentication or Client ID was rejected")
	ErrMQTTConnectionTimedOut       = errors.New("MQTT broker connection timed out")
	ErrPublisherMustBeDisabled      = errors.New("Data Publisher must be disabled")
)

type MQTTSecretOverride struct {
	Reference SecretReference
	Kind      SecretKind
	Material  SecretMaterial
}

type MQTTConnectionTestResult struct {
	Connected   bool          `json:"connected"`
	ConnectedAt time.Time     `json:"connected_at"`
	Latency     time.Duration `json:"-"`
	LatencyMS   int64         `json:"latency_ms"`
}

type MQTTConnectionTester struct {
	secrets SecretResolver
	engine  *JSONPayloadEngine
	options []MQTTTransportOption
	now     func() time.Time
}

func NewMQTTConnectionTester(secrets SecretResolver, engine *JSONPayloadEngine, options ...MQTTTransportOption) (*MQTTConnectionTester, error) {
	if isNilSourceDependency(secrets) || engine == nil {
		return nil, ErrMQTTConnectionTesterRequired
	}
	return &MQTTConnectionTester{secrets: secrets, engine: engine, options: append([]MQTTTransportOption(nil), options...), now: time.Now}, nil
}

func (tester *MQTTConnectionTester) Test(ctx context.Context, entity Publisher, sources []ResolvedSource, overrides []MQTTSecretOverride) (MQTTConnectionTestResult, error) {
	if tester == nil || tester.engine == nil || tester.now == nil || isNilSourceDependency(tester.secrets) || ctx == nil || entity.ID == uuid.Nil || entity.Type != TypeMQTT {
		return MQTTConnectionTestResult{}, ErrInvalidInput
	}
	if entity.Enabled {
		return MQTTConnectionTestResult{}, ErrPublisherMustBeDisabled
	}
	config, err := validateMQTTTransportPublisher(entity)
	if err != nil {
		return MQTTConnectionTestResult{}, err
	}
	resolver, err := newMQTTOverrideResolver(tester.secrets, overrides)
	if err != nil {
		return MQTTConnectionTestResult{}, err
	}
	defer resolver.destroy()
	factory, err := NewMQTTTransportFactory(resolver, tester.engine, tester.options...)
	if err != nil {
		return MQTTConnectionTestResult{}, err
	}
	startedAt := tester.now().UTC()
	transport, err := factory.NewResolvedTransport(ctx, entity, sources)
	if err != nil {
		return MQTTConnectionTestResult{}, err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = transport.Close(closeContext)
	}()
	metrics, ok := transport.(TransportMetricsProvider)
	if !ok {
		return MQTTConnectionTestResult{}, ErrMQTTConnectionTestFailed
	}
	timeout := time.Duration(config.MQTT.ConnectTimeoutMS)*time.Millisecond + 2*time.Second
	if timeout > 32*time.Second {
		timeout = 32 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := metrics.Metrics()
		if status.Connected {
			connectedAt := tester.now().UTC()
			latency := connectedAt.Sub(startedAt)
			return MQTTConnectionTestResult{Connected: true, ConnectedAt: connectedAt, Latency: latency, LatencyMS: latency.Milliseconds()}, nil
		}
		if status.TransportError != "" {
			return MQTTConnectionTestResult{}, classifyMQTTConnectionTestError(status.TransportError)
		}
		select {
		case <-ctx.Done():
			return MQTTConnectionTestResult{}, ctx.Err()
		case <-timer.C:
			return MQTTConnectionTestResult{}, ErrMQTTConnectionTimedOut
		case <-ticker.C:
		}
	}
}

func classifyMQTTConnectionTestError(message string) error {
	normalized := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(normalized, "no such host"),
		strings.Contains(normalized, "name resolution"),
		strings.Contains(normalized, "server misbehaving"):
		return ErrMQTTConnectionDNSFailed
	case strings.Contains(normalized, "bad user name or password"),
		strings.Contains(normalized, "not authorized"),
		strings.Contains(normalized, "not authorised"),
		strings.Contains(normalized, "identifier rejected"),
		strings.Contains(normalized, "client identifier"):
		return ErrMQTTConnectionAuthFailed
	case strings.Contains(normalized, "tls"),
		strings.Contains(normalized, "x509"),
		strings.Contains(normalized, "certificate"),
		strings.Contains(normalized, "handshake"):
		return ErrMQTTConnectionTLSFailed
	case strings.Contains(normalized, "connection refused"),
		strings.Contains(normalized, "network is unreachable"),
		strings.Contains(normalized, "no route to host"),
		strings.Contains(normalized, "connection reset"),
		strings.Contains(normalized, "network error"),
		strings.Contains(normalized, "dial tcp"),
		strings.Contains(normalized, "i/o timeout"):
		return ErrMQTTConnectionTCPFailed
	case strings.Contains(normalized, "timed out"), strings.Contains(normalized, "timeout"):
		return ErrMQTTConnectionTimedOut
	default:
		return ErrMQTTConnectionTestFailed
	}
}

type mqttOverrideResolver struct {
	base      SecretResolver
	overrides map[SecretReference]MQTTSecretOverride
}

func newMQTTOverrideResolver(base SecretResolver, overrides []MQTTSecretOverride) (*mqttOverrideResolver, error) {
	resolver := &mqttOverrideResolver{base: base, overrides: make(map[SecretReference]MQTTSecretOverride, len(overrides))}
	for _, override := range overrides {
		if override.Reference.Validate() != nil || !validSecretKind(override.Kind) || validateSecretMaterial(override.Kind, override.Material) != nil {
			resolver.destroy()
			return nil, ErrInvalidSecretMaterial
		}
		if _, exists := resolver.overrides[override.Reference]; exists {
			resolver.destroy()
			return nil, ErrInvalidInput
		}
		override.Material = override.Material.Clone()
		resolver.overrides[override.Reference] = override
	}
	return resolver, nil
}

func (resolver *mqttOverrideResolver) Resolve(ctx context.Context, publisherID uuid.UUID, reference SecretReference, expectedKind SecretKind) (SecretMaterial, SecretMetadata, error) {
	if override, exists := resolver.overrides[reference]; exists {
		if override.Kind != expectedKind {
			return SecretMaterial{}, SecretMetadata{}, ErrSecretKindMismatch
		}
		return override.Material.Clone(), SecretMetadata{PublisherID: publisherID, Reference: reference, Kind: expectedKind}, nil
	}
	return resolver.base.Resolve(ctx, publisherID, reference, expectedKind)
}

func (resolver *mqttOverrideResolver) destroy() {
	if resolver == nil {
		return
	}
	for reference, override := range resolver.overrides {
		override.Material.Destroy()
		delete(resolver.overrides, reference)
	}
}
