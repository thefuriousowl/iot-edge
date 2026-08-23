package publisher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMQTTConnectionTesterUsesTransientOverridesWithoutPublishing(t *testing.T) {
	publisherID := uuid.New()
	client := newFakeMQTTWireClient(true)
	base := &connectionSecretResolver{err: ErrSecretNotFound}
	tester, err := NewMQTTConnectionTester(base, NewJSONPayloadEngine(), WithMQTTWireFactory(func(settings mqttWireSettings) (mqttWireClient, error) {
		client.settings = settings
		return client, nil
	}))
	if err != nil {
		t.Fatalf("NewMQTTConnectionTester() error = %v", err)
	}
	entity := mqttTransportPublisher(t, publisherID, `{"value":{{value "value"}}}`, 8, 8)
	entity.Enabled = false
	sources := []ResolvedSource{{Alias: "value", Descriptor: SourceDescriptor{Reference: TagSource(uuid.New()), Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true}}}
	result, err := tester.Test(context.Background(), entity, sources, []MQTTSecretOverride{
		{Reference: SecretReference{Name: "mqtt.username"}, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: []byte("qa-user")}},
		{Reference: SecretReference{Name: "mqtt.password"}, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: []byte("qa-password")}},
	})
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if !result.Connected || result.ConnectedAt.IsZero() || result.LatencyMS < 0 {
		t.Fatalf("result = %#v", result)
	}
	if base.calls != 0 {
		t.Fatalf("persisted resolver calls = %d, want 0", base.calls)
	}
	select {
	case <-client.published:
		t.Fatal("connection test published telemetry")
	default:
	}
}

func TestMQTTConnectionTesterRejectsEnabledAndInvalidOverrides(t *testing.T) {
	base := &connectionSecretResolver{}
	tester, _ := NewMQTTConnectionTester(base, NewJSONPayloadEngine())
	entity := mqttTransportPublisher(t, uuid.New(), `{"value":{{value "value"}}}`, 8, 8)
	if _, err := tester.Test(context.Background(), entity, nil, nil); !errors.Is(err, ErrPublisherMustBeDisabled) {
		t.Fatalf("enabled Test() error = %v", err)
	}
	entity.Enabled = false
	_, err := tester.Test(context.Background(), entity, nil, []MQTTSecretOverride{{Reference: SecretReference{Name: "mqtt.password"}, Kind: SecretKindOpaque}})
	if !errors.Is(err, ErrInvalidSecretMaterial) {
		t.Fatalf("invalid override error = %v", err)
	}
}

func TestMQTTConnectionTesterReturnsSafeAuthenticationFailure(t *testing.T) {
	client := newFakeMQTTWireClient(false)
	client.connectError = errors.New("identifier rejected")
	tester, err := NewMQTTConnectionTester(&connectionSecretResolver{}, NewJSONPayloadEngine(), WithMQTTWireFactory(func(settings mqttWireSettings) (mqttWireClient, error) {
		client.settings = settings
		return client, nil
	}))
	if err != nil {
		t.Fatalf("NewMQTTConnectionTester() error = %v", err)
	}
	entity := mqttTransportPublisher(t, uuid.New(), `{"value":{{value "value"}}}`, 8, 8)
	entity.Enabled = false
	sources := []ResolvedSource{{Alias: "value", Descriptor: SourceDescriptor{Reference: TagSource(uuid.New()), Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true}}}
	_, err = tester.Test(context.Background(), entity, sources, nil)
	if !errors.Is(err, ErrMQTTConnectionAuthFailed) {
		t.Fatalf("Test() error = %v, want authentication classification", err)
	}
}

func TestClassifyMQTTConnectionTestError(t *testing.T) {
	for _, test := range []struct {
		message string
		want    error
	}{
		{message: "dial tcp: lookup broker.example: no such host", want: ErrMQTTConnectionDNSFailed},
		{message: "network Error: dial tcp 127.0.0.1:1883: connection refused", want: ErrMQTTConnectionTCPFailed},
		{message: "tls: failed to verify certificate: x509: certificate signed by unknown authority", want: ErrMQTTConnectionTLSFailed},
		{message: "bad user name or password", want: ErrMQTTConnectionAuthFailed},
		{message: "identifier rejected", want: ErrMQTTConnectionAuthFailed},
		{message: "MQTT connection timed out", want: ErrMQTTConnectionTimedOut},
		{message: "unexpected broker failure", want: ErrMQTTConnectionTestFailed},
	} {
		if got := classifyMQTTConnectionTestError(test.message); !errors.Is(got, test.want) {
			t.Errorf("classifyMQTTConnectionTestError(%q) = %v, want %v", test.message, got, test.want)
		}
	}
}

type connectionSecretResolver struct {
	calls int
	err   error
}

func (resolver *connectionSecretResolver) Resolve(context.Context, uuid.UUID, SecretReference, SecretKind) (SecretMaterial, SecretMetadata, error) {
	resolver.calls++
	if resolver.err != nil {
		return SecretMaterial{}, SecretMetadata{}, resolver.err
	}
	return SecretMaterial{Opaque: []byte("saved")}, SecretMetadata{}, nil
}

func TestMQTTConnectionResultLatencyUsesMilliseconds(t *testing.T) {
	result := MQTTConnectionTestResult{Latency: 25 * time.Millisecond, LatencyMS: (25 * time.Millisecond).Milliseconds()}
	if result.LatencyMS != 25 {
		t.Fatalf("LatencyMS = %d", result.LatencyMS)
	}
}
