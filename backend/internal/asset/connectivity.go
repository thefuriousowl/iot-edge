package asset

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrConnectivityUnavailable    = errors.New("Asset connectivity projection is unavailable")
	ErrConnectivitySourceNotFound = errors.New("Asset connectivity source not found")
)

type ConnectivityEntity struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Enabled bool      `json:"enabled"`
}

type MeasurementConnectivity struct {
	BindingID  uuid.UUID           `json:"binding_id"`
	Source     SourceReference     `json:"source"`
	Tag        *ConnectivityEntity `json:"tag,omitempty"`
	Datasource *ConnectivityEntity `json:"datasource,omitempty"`
	Device     *ConnectivityEntity `json:"device,omitempty"`
	VGateway   *ConnectivityEntity `json:"vgateway,omitempty"`
}

type AssetConnectivity struct {
	AssetID uuid.UUID                 `json:"asset_id"`
	Links   []MeasurementConnectivity `json:"links"`
}

type TagAssetLink struct {
	BindingID uuid.UUID `json:"binding_id"`
	Asset     Asset     `json:"asset"`
	Semantic  Semantic  `json:"semantic"`
}

type TagAssetConnectivity struct {
	TagID  uuid.UUID      `json:"tag_id"`
	Assets []TagAssetLink `json:"assets"`
}
