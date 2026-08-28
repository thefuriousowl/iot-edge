package sourcecatalog

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/utility"
)

var (
	ErrAssetRepositoryRequired = errors.New("utility source catalog Asset repository is required")
	ErrSourceCatalogRequired   = errors.New("utility source descriptor catalog is required")
)

type Catalog struct {
	assets  asset.Repository
	sources asset.SourceCatalog
}

func New(assets asset.Repository, sources asset.SourceCatalog) (*Catalog, error) {
	if isNil(assets) {
		return nil, ErrAssetRepositoryRequired
	}
	if isNil(sources) {
		return nil, ErrSourceCatalogRequired
	}
	return &Catalog{assets: assets, sources: sources}, nil
}

func (catalog *Catalog) Describe(ctx context.Context, ownerID uuid.UUID, reference asset.SourceReference) (utility.SourceDescriptor, error) {
	if ctx == nil || ownerID == uuid.Nil || reference.Validate() != nil {
		return utility.SourceDescriptor{}, utility.ErrSourceUnavailable
	}
	bindings, err := catalog.assets.ListBindings(ctx, ownerID)
	if err != nil {
		return utility.SourceDescriptor{}, err
	}
	found := false
	for _, binding := range bindings {
		if binding.Source == reference {
			found = true
			break
		}
	}
	if !found {
		return utility.SourceDescriptor{}, utility.ErrSourceUnavailable
	}
	descriptor, err := catalog.sources.Describe(ctx, reference)
	if err != nil {
		return utility.SourceDescriptor{}, err
	}
	ancestors, err := catalog.assets.Ancestors(ctx, ownerID)
	if err != nil {
		return utility.SourceDescriptor{}, err
	}
	ancestorIDs := make([]uuid.UUID, len(ancestors))
	for index, ancestor := range ancestors {
		ancestorIDs[index] = ancestor.ID
	}
	return utility.SourceDescriptor{Reference: descriptor.Reference, OwnerAssetID: ownerID, OwnerAncestors: ancestorIDs, DataType: descriptor.DataType, Unit: descriptor.Unit, DynamicUnit: descriptor.DynamicUnit}, nil
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

var _ utility.SourceCatalog = (*Catalog)(nil)
