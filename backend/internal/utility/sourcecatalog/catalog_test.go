package sourcecatalog

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/utility"
)

func TestCatalogResolvesOnlyPersistedOwnerBindingAndAncestry(t *testing.T) {
	t.Parallel()
	siteID, areaID, meterID, tagID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	reference := asset.TagSource(tagID)
	repository := &assetRepository{
		ancestors: []asset.Asset{{ID: siteID}, {ID: areaID}},
		bindings:  map[uuid.UUID][]asset.MeasurementBinding{meterID: {{OwnerAssetID: meterID, Source: reference}}},
	}
	sources := &assetSources{descriptor: asset.SourceDescriptor{Reference: reference, DataType: "float64", Unit: asset.UnitKilowatt}}
	catalog, err := New(repository, sources)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := catalog.Describe(context.Background(), meterID, reference)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.OwnerAssetID != meterID || len(descriptor.OwnerAncestors) != 2 || descriptor.OwnerAncestors[0] != siteID || descriptor.OwnerAncestors[1] != areaID || descriptor.Unit != asset.UnitKilowatt {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if _, err := catalog.Describe(context.Background(), uuid.New(), reference); !errors.Is(err, utility.ErrSourceUnavailable) {
		t.Fatalf("unbound owner error = %v", err)
	}
}

func TestCatalogValidatesDependenciesAndPropagatesLookups(t *testing.T) {
	t.Parallel()
	repository, sources := &assetRepository{}, &assetSources{}
	if _, err := New(nil, sources); !errors.Is(err, ErrAssetRepositoryRequired) {
		t.Fatalf("nil repository error = %v", err)
	}
	if _, err := New(repository, nil); !errors.Is(err, ErrSourceCatalogRequired) {
		t.Fatalf("nil sources error = %v", err)
	}
	repository.err = context.Canceled
	catalog, _ := New(repository, sources)
	if _, err := catalog.Describe(context.Background(), uuid.New(), asset.TagSource(uuid.New())); !errors.Is(err, context.Canceled) {
		t.Fatalf("repository error = %v", err)
	}
}

type assetSources struct {
	descriptor asset.SourceDescriptor
	err        error
}

func (sources *assetSources) Describe(context.Context, asset.SourceReference) (asset.SourceDescriptor, error) {
	return sources.descriptor, sources.err
}

type assetRepository struct {
	bindings  map[uuid.UUID][]asset.MeasurementBinding
	ancestors []asset.Asset
	err       error
}

func (*assetRepository) Create(context.Context, *asset.Asset) error {
	return errors.New("not implemented")
}
func (*assetRepository) Find(context.Context, uuid.UUID) (*asset.Asset, error) {
	return nil, errors.New("not implemented")
}
func (*assetRepository) List(context.Context, asset.ListInput) (*asset.ListResult, error) {
	return nil, errors.New("not implemented")
}
func (*assetRepository) ListChildren(context.Context, *uuid.UUID) ([]asset.Asset, error) {
	return nil, errors.New("not implemented")
}
func (*assetRepository) Subtree(context.Context, uuid.UUID) (*asset.TreeNode, error) {
	return nil, errors.New("not implemented")
}
func (repository *assetRepository) Ancestors(context.Context, uuid.UUID) ([]asset.Asset, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	return append([]asset.Asset(nil), repository.ancestors...), nil
}
func (*assetRepository) Update(context.Context, *asset.Asset) error {
	return errors.New("not implemented")
}
func (*assetRepository) Move(context.Context, uuid.UUID, *uuid.UUID, int) error {
	return errors.New("not implemented")
}
func (*assetRepository) Delete(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}
func (repository *assetRepository) ListBindings(_ context.Context, id uuid.UUID) ([]asset.MeasurementBinding, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	return append([]asset.MeasurementBinding(nil), repository.bindings[id]...), nil
}
func (*assetRepository) ListBindingsByTag(context.Context, uuid.UUID) ([]asset.MeasurementBinding, error) {
	return nil, errors.New("not implemented")
}
func (*assetRepository) ReplaceBindings(context.Context, uuid.UUID, []asset.MeasurementBinding) error {
	return errors.New("not implemented")
}
