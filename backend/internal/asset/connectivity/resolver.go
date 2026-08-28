package connectivity

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/device"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
)

var (
	ErrAssetRepositoryRequired = errors.New("Asset connectivity repository is required")
	ErrTagReaderRequired       = errors.New("Asset connectivity Tag reader is required")
	ErrDeviceReaderRequired    = errors.New("Asset connectivity Device reader is required")
	ErrGatewayReaderRequired   = errors.New("Asset connectivity vGateway reader is required")
)

type AssetRepository interface {
	Find(context.Context, uuid.UUID) (*asset.Asset, error)
	ListBindings(context.Context, uuid.UUID) ([]asset.MeasurementBinding, error)
	ListBindingsByTag(context.Context, uuid.UUID) ([]asset.MeasurementBinding, error)
}

type TagReader interface {
	Get(context.Context, uuid.UUID) (*tag.Tag, error)
}

type DeviceReader interface {
	GetDatasource(context.Context, uuid.UUID) (*device.DatasourceView, error)
	GetDevice(context.Context, uuid.UUID) (*device.DeviceView, error)
}

type GatewayReader interface {
	Get(context.Context, uuid.UUID) (*vgateway.VGatewayView, error)
}

type Resolver struct {
	assets   AssetRepository
	tags     TagReader
	devices  DeviceReader
	gateways GatewayReader
}

func NewResolver(assets AssetRepository, tags TagReader, devices DeviceReader, gateways GatewayReader) (*Resolver, error) {
	if isNil(assets) {
		return nil, ErrAssetRepositoryRequired
	}
	if isNil(tags) {
		return nil, ErrTagReaderRequired
	}
	if isNil(devices) {
		return nil, ErrDeviceReaderRequired
	}
	if isNil(gateways) {
		return nil, ErrGatewayReaderRequired
	}
	return &Resolver{assets: assets, tags: tags, devices: devices, gateways: gateways}, nil
}

func (resolver *Resolver) Asset(ctx context.Context, assetID uuid.UUID) (*asset.AssetConnectivity, error) {
	if ctx == nil || assetID == uuid.Nil {
		return nil, asset.ErrInvalidAssetInput
	}
	if _, err := resolver.assets.Find(ctx, assetID); err != nil {
		return nil, err
	}
	bindings, err := resolver.assets.ListBindings(ctx, assetID)
	if err != nil {
		return nil, err
	}
	result := &asset.AssetConnectivity{AssetID: assetID, Links: make([]asset.MeasurementConnectivity, len(bindings))}
	for index, binding := range bindings {
		link := asset.MeasurementConnectivity{BindingID: binding.ID, Source: binding.Source}
		if binding.Source.Kind == asset.SourceTag {
			if err := resolver.populateTag(ctx, binding.Source.TagID, &link); err != nil {
				return nil, err
			}
		}
		result.Links[index] = link
	}
	return result, nil
}

func (resolver *Resolver) Tag(ctx context.Context, tagID uuid.UUID) (*asset.TagAssetConnectivity, error) {
	if ctx == nil || tagID == uuid.Nil {
		return nil, asset.ErrInvalidBinding
	}
	if _, err := resolver.tags.Get(ctx, tagID); err != nil {
		if errors.Is(err, tag.ErrTagNotFound) {
			return nil, asset.ErrConnectivitySourceNotFound
		}
		return nil, err
	}
	bindings, err := resolver.assets.ListBindingsByTag(ctx, tagID)
	if err != nil {
		return nil, err
	}
	result := &asset.TagAssetConnectivity{TagID: tagID, Assets: make([]asset.TagAssetLink, len(bindings))}
	for index, binding := range bindings {
		owner, findErr := resolver.assets.Find(ctx, binding.OwnerAssetID)
		if findErr != nil {
			return nil, findErr
		}
		result.Assets[index] = asset.TagAssetLink{BindingID: binding.ID, Asset: *owner, Semantic: binding.Semantic}
	}
	return result, nil
}

func (resolver *Resolver) populateTag(ctx context.Context, tagID uuid.UUID, link *asset.MeasurementConnectivity) error {
	entity, err := resolver.tags.Get(ctx, tagID)
	if err != nil {
		if errors.Is(err, tag.ErrTagNotFound) {
			return asset.ErrConnectivitySourceNotFound
		}
		return err
	}
	link.Tag = &asset.ConnectivityEntity{ID: entity.ID, Name: entity.Name, Kind: string(entity.Type), Enabled: entity.Enabled}
	if entity.DatasourceID == nil {
		return nil
	}
	datasource, err := resolver.devices.GetDatasource(ctx, *entity.DatasourceID)
	if err != nil {
		return mapConnectivityError(err)
	}
	link.Datasource = &asset.ConnectivityEntity{ID: datasource.ID, Name: datasource.Name, Kind: string(datasource.Type), Enabled: datasource.Enabled}
	deviceView, err := resolver.devices.GetDevice(ctx, datasource.DeviceID)
	if err != nil {
		return mapConnectivityError(err)
	}
	link.Device = &asset.ConnectivityEntity{ID: deviceView.ID, Name: deviceView.Name, Kind: string(deviceView.Type), Enabled: deviceView.Enabled}
	gateway, err := resolver.gateways.Get(ctx, deviceView.VGatewayID)
	if err != nil {
		return mapConnectivityError(err)
	}
	link.VGateway = &asset.ConnectivityEntity{ID: gateway.ID, Name: gateway.Name, Kind: string(gateway.Type), Enabled: gateway.Enabled}
	return nil
}

func mapConnectivityError(err error) error {
	if errors.Is(err, device.ErrDatasourceNotFound) || errors.Is(err, device.ErrDeviceNotFound) || errors.Is(err, vgateway.ErrVGatewayNotFound) {
		return asset.ErrConnectivityUnavailable
	}
	return err
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
