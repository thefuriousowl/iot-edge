package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPServerEndpointProjectsOnlyPublicListenerMetadata(t *testing.T) {
	entity, _ := httpTransportPublisher(t, uuid.New(), HTTPAccessConfig{Mode: HTTPAccessAPIKey, APIKeyHeader: "X-Plant-Key"}, HTTPQualityStrict)
	endpoint, err := HTTPServerEndpoint(entity)
	if err != nil {
		t.Fatalf("HTTPServerEndpoint() error = %v", err)
	}
	if endpoint.Network != "tcp" || endpoint.Scheme != "http" || endpoint.BindAddress != "127.0.0.1" || endpoint.Port != 8088 || endpoint.Path != "/snapshot" || endpoint.AccessMode != HTTPAccessAPIKey || endpoint.QualityPolicy != HTTPQualityStrict {
		t.Fatalf("HTTPServerEndpoint() = %#v", endpoint)
	}
	encoded, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"X-Plant-Key", "payload_template", "credential", "secret"} {
		if stringContains(string(encoded), forbidden) {
			t.Fatalf("endpoint metadata leaked %q: %s", forbidden, encoded)
		}
	}

	entity.Type = TypeMQTT
	if _, err := HTTPServerEndpoint(entity); !errors.Is(err, ErrInvalidPublisher) {
		t.Fatalf("MQTT endpoint error = %v, want %v", err, ErrInvalidPublisher)
	}
}

func TestHTTPServerProberUsesLoopbackForWildcardWithoutSendingHTTP(t *testing.T) {
	entity, _ := httpTransportPublisher(t, uuid.New(), HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	config, err := ParseHTTPPublisherConfig(entity.Config)
	if err != nil {
		t.Fatalf("ParseHTTPPublisherConfig() error = %v", err)
	}
	config.HTTP.BindAddress = "0.0.0.0"
	encoded, _ := json.Marshal(config)
	entity.Config, err = normalizeHTTPPublisherConfig(encoded)
	if err != nil {
		t.Fatalf("normalizeHTTPPublisherConfig() error = %v", err)
	}
	startedAt := time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(1250 * time.Microsecond)
	clockValues := []time.Time{startedAt, finishedAt}
	var network, address string
	prober, err := NewHTTPServerProber(
		WithHTTPServerProbeClock(func() time.Time {
			value := clockValues[0]
			clockValues = clockValues[1:]
			return value
		}),
		WithHTTPServerProbeDialer(func(_ context.Context, receivedNetwork, receivedAddress string) (net.Conn, error) {
			network, address = receivedNetwork, receivedAddress
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPServerProber() error = %v", err)
	}
	result, err := prober.Probe(context.Background(), entity)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !result.Reachable || !result.ProbedAt.Equal(finishedAt) || result.LatencyMS != 1.25 || network != "tcp" || address != "127.0.0.1:8088" || result.Endpoint.BindAddress != "0.0.0.0" {
		t.Fatalf("Probe() = %#v, dial=%s %s", result, network, address)
	}
}

func TestHTTPServerProberMapsFailuresWithoutLeakingDialErrors(t *testing.T) {
	entity, _ := httpTransportPublisher(t, uuid.New(), HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	privateError := errors.New("dial tcp token=private-value")
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "failure", err: privateError, want: ErrHTTPServerProbeFailed},
		{name: "timeout", err: context.DeadlineExceeded, want: ErrHTTPServerProbeTimedOut},
	} {
		t.Run(test.name, func(t *testing.T) {
			prober, err := NewHTTPServerProber(WithHTTPServerProbeDialer(func(context.Context, string, string) (net.Conn, error) {
				return nil, test.err
			}))
			if err != nil {
				t.Fatalf("NewHTTPServerProber() error = %v", err)
			}
			_, err = prober.Probe(context.Background(), entity)
			if !errors.Is(err, test.want) || stringContains(err.Error(), "private-value") {
				t.Fatalf("Probe() error = %v, want sanitized %v", err, test.want)
			}
		})
	}
}

func TestHTTPServerProberPreservesCallerCancellation(t *testing.T) {
	entity, _ := httpTransportPublisher(t, uuid.New(), HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prober, err := NewHTTPServerProber()
	if err != nil {
		t.Fatalf("NewHTTPServerProber() error = %v", err)
	}
	if _, err := prober.Probe(ctx, entity); !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe(cancelled) error = %v", err)
	}
}

func stringContains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
