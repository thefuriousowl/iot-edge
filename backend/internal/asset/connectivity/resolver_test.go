package connectivity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/device"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
)

type assetRepository struct {
	assets   map[uuid.UUID]asset.Asset
	bindings map[uuid.UUID][]asset.MeasurementBinding
}

func (repository *assetRepository) Find(_ context.Context, id uuid.UUID) (*asset.Asset, error) {
	value, exists := repository.assets[id]
	if !exists {
		return nil, asset.ErrAssetNotFound
	}
	return &value, nil
}
func (repository *assetRepository) ListBindings(_ context.Context, id uuid.UUID) ([]asset.MeasurementBinding, error) {
	return append([]asset.MeasurementBinding(nil), repository.bindings[id]...), nil
}
func (repository *assetRepository) ListBindingsByTag(_ context.Context, id uuid.UUID) ([]asset.MeasurementBinding, error) {
	values := []asset.MeasurementBinding{}
	for _, bindings := range repository.bindings {
		for _, binding := range bindings {
			if binding.Source.Kind == asset.SourceTag && binding.Source.TagID == id {
				values = append(values, binding)
			}
		}
	}
	return values, nil
}

type tagReader struct{ values map[uuid.UUID]tag.Tag }

func (reader *tagReader) Get(_ context.Context, id uuid.UUID) (*tag.Tag, error) {
	value, exists := reader.values[id]
	if !exists {
		return nil, tag.ErrTagNotFound
	}
	return &value, nil
}

type deviceReader struct {
	datasources map[uuid.UUID]device.DatasourceView
	devices     map[uuid.UUID]device.DeviceView
}

func (reader *deviceReader) GetDatasource(_ context.Context, id uuid.UUID) (*device.DatasourceView, error) {
	value, exists := reader.datasources[id]
	if !exists {
		return nil, device.ErrDatasourceNotFound
	}
	return &value, nil
}
func (reader *deviceReader) GetDevice(_ context.Context, id uuid.UUID) (*device.DeviceView, error) {
	value, exists := reader.devices[id]
	if !exists {
		return nil, device.ErrDeviceNotFound
	}
	return &value, nil
}

type gatewayReader struct {
	values map[uuid.UUID]vgateway.VGatewayView
}

func (reader *gatewayReader) Get(_ context.Context, id uuid.UUID) (*vgateway.VGatewayView, error) {
	value, exists := reader.values[id]
	if !exists {
		return nil, vgateway.ErrVGatewayNotFound
	}
	return &value, nil
}

func TestResolverProjectsFullConnectivityAndNonPhysicalSources(t *testing.T) {
	assetID, tagID, constantID := uuid.New(), uuid.New(), uuid.New()
	datasourceID, deviceID, gatewayID, pluginID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	bindings := []asset.MeasurementBinding{
		connectivityBinding(assetID, asset.TagSource(tagID)),
		connectivityBinding(assetID, asset.TagSource(constantID)),
		connectivityBinding(assetID, asset.PluginOutputSource(pluginID, "power")),
	}
	repository := &assetRepository{assets: map[uuid.UUID]asset.Asset{assetID: {ID: assetID, Name: "Compressor", Kind: asset.KindEquipment, Enabled: true}}, bindings: map[uuid.UUID][]asset.MeasurementBinding{assetID: bindings}}
	tags := &tagReader{values: map[uuid.UUID]tag.Tag{
		tagID:      {ID: tagID, DatasourceID: &datasourceID, Name: "Pressure", Type: tag.TypeReading, Enabled: true},
		constantID: {ID: constantID, Name: "Setpoint", Type: tag.TypeConstant, Enabled: true},
	}}
	devices := &deviceReader{
		datasources: map[uuid.UUID]device.DatasourceView{datasourceID: {Datasource: device.Datasource{ID: datasourceID, DeviceID: deviceID, Name: "Holding registers", Type: device.DatasourceTypeModbusRead, Enabled: true}}},
		devices:     map[uuid.UUID]device.DeviceView{deviceID: {Device: device.Device{ID: deviceID, VGatewayID: gatewayID, Name: "PLC", Type: device.DeviceTypeModbus, Enabled: true}}},
	}
	gateways := &gatewayReader{values: map[uuid.UUID]vgateway.VGatewayView{gatewayID: {VGateway: vgateway.VGateway{ID: gatewayID, Name: "Plant gateway", Type: vgateway.VGatewayTypeModbusTCP, Enabled: true}}}}
	resolver, err := NewResolver(repository, tags, devices, gateways)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.Asset(context.Background(), assetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Links) != 3 || result.Links[0].Tag == nil || result.Links[0].Datasource == nil || result.Links[0].Device == nil || result.Links[0].VGateway == nil || result.Links[0].VGateway.ID != gatewayID {
		t.Fatalf("reading connectivity = %#v", result.Links[0])
	}
	if result.Links[1].Tag == nil || result.Links[1].Datasource != nil || result.Links[1].Tag.ID != constantID {
		t.Fatalf("constant connectivity = %#v", result.Links[1])
	}
	if result.Links[2].Source.Kind != asset.SourcePluginOutput || result.Links[2].Tag != nil {
		t.Fatalf("Plugin connectivity = %#v", result.Links[2])
	}

	reverse, err := resolver.Tag(context.Background(), tagID)
	if err != nil {
		t.Fatal(err)
	}
	if reverse.TagID != tagID || len(reverse.Assets) != 1 || reverse.Assets[0].Asset.ID != assetID || reverse.Assets[0].BindingID != bindings[0].ID {
		t.Fatalf("reverse connectivity = %#v", reverse)
	}
}

func TestResolverMapsMissingAndIncompleteConnectivity(t *testing.T) {
	assetID, tagID, datasourceID := uuid.New(), uuid.New(), uuid.New()
	binding := connectivityBinding(assetID, asset.TagSource(tagID))
	repository := &assetRepository{assets: map[uuid.UUID]asset.Asset{assetID: {ID: assetID}}, bindings: map[uuid.UUID][]asset.MeasurementBinding{assetID: {binding}}}
	tags := &tagReader{values: map[uuid.UUID]tag.Tag{tagID: {ID: tagID, DatasourceID: &datasourceID, Type: tag.TypeReading}}}
	resolver, _ := NewResolver(repository, tags, &deviceReader{}, &gatewayReader{})
	if _, err := resolver.Asset(context.Background(), assetID); !errors.Is(err, asset.ErrConnectivityUnavailable) {
		t.Fatalf("incomplete chain error = %v", err)
	}
	if _, err := resolver.Tag(context.Background(), uuid.New()); !errors.Is(err, asset.ErrConnectivitySourceNotFound) {
		t.Fatalf("missing Tag error = %v", err)
	}
}

func TestResolverRequiresDependencies(t *testing.T) {
	repository := &assetRepository{}
	tags := &tagReader{}
	devices := &deviceReader{}
	gateways := &gatewayReader{}
	if _, err := NewResolver(nil, tags, devices, gateways); !errors.Is(err, ErrAssetRepositoryRequired) {
		t.Fatalf("nil assets error = %v", err)
	}
	if _, err := NewResolver(repository, nil, devices, gateways); !errors.Is(err, ErrTagReaderRequired) {
		t.Fatalf("nil tags error = %v", err)
	}
	if _, err := NewResolver(repository, tags, nil, gateways); !errors.Is(err, ErrDeviceReaderRequired) {
		t.Fatalf("nil devices error = %v", err)
	}
	if _, err := NewResolver(repository, tags, devices, nil); !errors.Is(err, ErrGatewayReaderRequired) {
		t.Fatalf("nil gateways error = %v", err)
	}
}

func connectivityBinding(owner uuid.UUID, source asset.SourceReference) asset.MeasurementBinding {
	return asset.MeasurementBinding{ID: uuid.New(), OwnerAssetID: owner, BoundaryAssetID: owner, Source: source, Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt}, MeterRole: asset.MeterRoleDirect, RollupPolicy: asset.RollupInclude}
}
