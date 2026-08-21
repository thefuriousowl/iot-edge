package vgateway

import (
	"context"

	"github.com/google/uuid"
)

type VGatewayListOptions struct {
	Type    *VGatewayType
	Enabled *bool
	Limit   int
	Offset  int
}

type VGatewayRepository interface {
	Create(ctx context.Context, gateway *VGateway) error
	FindByID(ctx context.Context, id uuid.UUID) (*VGateway, error)
	List(
		ctx context.Context,
		options VGatewayListOptions,
	) ([]VGateway, int64, error)
	Update(ctx context.Context, gateway *VGateway) error
	Delete(ctx context.Context, id uuid.UUID) error
}
