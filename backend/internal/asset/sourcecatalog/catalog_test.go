package sourcecatalog

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

type tagFinder struct {
	values map[uuid.UUID]tag.Tag
	err    error
}

func (finder *tagFinder) Find(_ context.Context, id uuid.UUID) (*tag.Tag, error) {
	if finder.err != nil {
		return nil, finder.err
	}
	value, exists := finder.values[id]
	if !exists {
		return nil, tag.ErrTagNotFound
	}
	return &value, nil
}

type outputResolver struct {
	values map[uuid.UUID][]plugin.OutputDescriptor
	err    error
}

func (resolver *outputResolver) OutputDescriptors(_ context.Context, id uuid.UUID) ([]plugin.OutputDescriptor, error) {
	if resolver.err != nil {
		return nil, resolver.err
	}
	return append([]plugin.OutputDescriptor(nil), resolver.values[id]...), nil
}

func TestNewRequiresDependencies(t *testing.T) {
	finder := &tagFinder{}
	resolver := &outputResolver{}
	if _, err := New(nil, resolver); !errors.Is(err, ErrTagFinderRequired) {
		t.Fatalf("New(nil finder) error = %v", err)
	}
	var typedNil *outputResolver
	if _, err := New(finder, typedNil); !errors.Is(err, ErrPluginOutputResolverRequired) {
		t.Fatalf("New(typed nil resolver) error = %v", err)
	}
}

func TestDescribeResolvesTagDataType(t *testing.T) {
	id := uuid.New()
	catalog, _ := New(&tagFinder{values: map[uuid.UUID]tag.Tag{id: {ID: id, DataType: tag.DataTypeUInt32}}}, &outputResolver{})
	reference := asset.TagSource(id)
	descriptor, err := catalog.Describe(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Reference != reference || descriptor.DataType != "uint32" || descriptor.Unit != "" || descriptor.DynamicUnit {
		t.Fatalf("unexpected Tag descriptor: %+v", descriptor)
	}
}

func TestDescribeResolvesExactPluginOutputContract(t *testing.T) {
	id := uuid.New()
	resolver := &outputResolver{values: map[uuid.UUID][]plugin.OutputDescriptor{id: {
		{Key: "power", DataType: plugin.OutputDataTypeFloat64, Unit: "kW"},
		{Key: "runtime", DataType: plugin.OutputDataTypeBool, DynamicUnit: true},
	}}}
	catalog, _ := New(&tagFinder{}, resolver)
	reference := asset.PluginOutputSource(id, "runtime")
	descriptor, err := catalog.Describe(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Reference != reference || descriptor.DataType != "bool" || descriptor.Unit != "" || !descriptor.DynamicUnit {
		t.Fatalf("unexpected Plugin descriptor: %+v", descriptor)
	}
	if _, err := catalog.Describe(context.Background(), asset.PluginOutputSource(id, "missing")); !errors.Is(err, asset.ErrBindingSourceMissing) {
		t.Fatalf("missing output error = %v", err)
	}
}

func TestDescribeRejectsInvalidReferenceAndPropagatesResolverErrors(t *testing.T) {
	want := errors.New("lookup failed")
	catalog, _ := New(&tagFinder{err: want}, &outputResolver{})
	if _, err := catalog.Describe(context.Background(), asset.SourceReference{}); !errors.Is(err, asset.ErrInvalidBinding) {
		t.Fatalf("invalid reference error = %v", err)
	}
	if _, err := catalog.Describe(context.Background(), asset.TagSource(uuid.New())); !errors.Is(err, want) {
		t.Fatalf("finder error = %v", err)
	}
}

func TestDescribeMapsMissingSourcesToAssetError(t *testing.T) {
	catalog, _ := New(&tagFinder{values: map[uuid.UUID]tag.Tag{}}, &outputResolver{err: plugin.ErrInstanceNotFound})
	if _, err := catalog.Describe(context.Background(), asset.TagSource(uuid.New())); !errors.Is(err, asset.ErrBindingSourceMissing) {
		t.Fatalf("missing Tag error = %v", err)
	}
	if _, err := catalog.Describe(context.Background(), asset.PluginOutputSource(uuid.New(), "power")); !errors.Is(err, asset.ErrBindingSourceMissing) {
		t.Fatalf("missing Plugin error = %v", err)
	}
}
