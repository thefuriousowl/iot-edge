package publisher

import (
	"context"
	"errors"
	"testing"
)

func TestTransportRegistryValidatesFactories(t *testing.T) {
	t.Parallel()

	registry := NewTransportRegistry()
	var typedNil *managerTransportFactory
	if err := registry.Register(TypeHTTPServer, typedNil); !errors.Is(err, ErrTransportRequired) {
		t.Fatalf("Register(typed nil) error = %v", err)
	}
	factory := &managerTransportFactory{}
	if err := registry.Register(TypeHTTPServer, factory); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Register(TypeHTTPServer, factory); !errors.Is(err, ErrTransportFactoryExists) {
		t.Fatalf("Register(duplicate) error = %v", err)
	}
	if _, err := registry.Find(TypeMQTT); !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("Find(missing) error = %v", err)
	}
	if _, err := (*TransportRegistry)(nil).Find(TypeMQTT); !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("nil Find() error = %v", err)
	}
}

func TestListenerClaimsNormalizeAndDetectOverlap(t *testing.T) {
	t.Parallel()

	claims, err := normalizeListenerClaims(TypeHTTPServer, []ListenerClaim{{Network: " TCP ", Address: ":8080"}})
	if err != nil || len(claims) != 1 || claims[0].Network != "tcp" || claims[0].Address != "*:8080" {
		t.Fatalf("normalizeListenerClaims() = %#v, %v", claims, err)
	}
	if !listenerClaimsConflict(claims[0], ListenerClaim{Network: "tcp4", Address: "127.0.0.1:8080"}) {
		t.Fatal("wildcard tcp listener did not conflict with tcp4 address")
	}
	if listenerClaimsConflict(claims[0], ListenerClaim{Network: "tcp", Address: "127.0.0.1:8081"}) {
		t.Fatal("different ports conflicted")
	}
	for _, test := range []struct {
		publisherType Type
		claims        []ListenerClaim
	}{
		{publisherType: TypeMQTT, claims: []ListenerClaim{{Network: "tcp", Address: ":1883"}}},
		{publisherType: TypeHTTPServer, claims: []ListenerClaim{{Network: "udp", Address: ":8080"}}},
		{publisherType: TypeHTTPServer, claims: []ListenerClaim{{Network: "tcp", Address: "8080"}}},
		{publisherType: TypeHTTPServer, claims: []ListenerClaim{{Network: "tcp", Address: ":0"}}},
		{publisherType: TypeHTTPServer, claims: []ListenerClaim{{Network: "tcp", Address: ":8080"}, {Network: "tcp4", Address: "127.0.0.1:8080"}}},
	} {
		if _, err := normalizeListenerClaims(test.publisherType, test.claims); !errors.Is(err, ErrInvalidListenerClaim) {
			t.Errorf("normalizeListenerClaims(%s, %#v) error = %v", test.publisherType, test.claims, err)
		}
	}
}

type nilManagerTransport struct{}

func (*nilManagerTransport) Publish(context.Context, SourceSnapshot) error { return nil }
func (*nilManagerTransport) Close(context.Context) error                   { return nil }
