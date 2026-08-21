package protocol

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrGatewayClientFactoryRequired = errors.New(
		"gateway client factory is required",
	)
	ErrInvalidGatewayConfig = errors.New(
		"invalid gateway config",
	)
	ErrGatewayClientRequired = errors.New(
		"gateway client is required",
	)
)

type GatewayClient interface {
	Connect(ctx context.Context) error
	Disconnect() error
	IsConnected() bool
}

// GatewayDriver is the protocol-specific boundary used by VGatewayService.
// It converts an API/persistence JSON object into a canonical validated form
// and creates a lifecycle client without exposing protocol details upstream.
type GatewayDriver interface {
	NormalizeConfig(raw json.RawMessage) (json.RawMessage, error)
	NewClient(config json.RawMessage) (GatewayClient, error)
}
