package sourcecatalog

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

var (
	ErrTagFinderRequired            = errors.New("asset source catalog Tag finder is required")
	ErrPluginOutputResolverRequired = errors.New("asset source catalog Plugin output resolver is required")
)

type TagFinder interface {
	Find(context.Context, uuid.UUID) (*tag.Tag, error)
}

type PluginOutputResolver interface {
	OutputDescriptors(context.Context, uuid.UUID) ([]plugin.OutputDescriptor, error)
}

type Catalog struct {
	tags    TagFinder
	outputs PluginOutputResolver
}

func New(tags TagFinder, outputs PluginOutputResolver) (*Catalog, error) {
	if isNilDependency(tags) {
		return nil, ErrTagFinderRequired
	}
	if isNilDependency(outputs) {
		return nil, ErrPluginOutputResolverRequired
	}
	return &Catalog{tags: tags, outputs: outputs}, nil
}

func (catalog *Catalog) Describe(ctx context.Context, reference asset.SourceReference) (asset.SourceDescriptor, error) {
	if ctx == nil || reference.Validate() != nil {
		return asset.SourceDescriptor{}, asset.ErrInvalidBinding
	}
	switch reference.Kind {
	case asset.SourceTag:
		entity, err := catalog.tags.Find(ctx, reference.TagID)
		if err != nil {
			if errors.Is(err, tag.ErrTagNotFound) {
				return asset.SourceDescriptor{}, asset.ErrBindingSourceMissing
			}
			return asset.SourceDescriptor{}, err
		}
		return asset.SourceDescriptor{Reference: reference, DataType: string(entity.DataType)}, nil
	case asset.SourcePluginOutput:
		descriptors, err := catalog.outputs.OutputDescriptors(ctx, reference.PluginInstanceID)
		if err != nil {
			if errors.Is(err, plugin.ErrInstanceNotFound) {
				return asset.SourceDescriptor{}, asset.ErrBindingSourceMissing
			}
			return asset.SourceDescriptor{}, err
		}
		for _, descriptor := range descriptors {
			if string(descriptor.Key) == reference.OutputKey {
				return asset.SourceDescriptor{Reference: reference, DataType: string(descriptor.DataType), Unit: asset.Unit(descriptor.Unit), DynamicUnit: descriptor.DynamicUnit}, nil
			}
		}
		return asset.SourceDescriptor{}, asset.ErrBindingSourceMissing
	default:
		return asset.SourceDescriptor{}, asset.ErrInvalidBinding
	}
}

func isNilDependency(value any) bool {
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

var _ asset.SourceCatalog = (*Catalog)(nil)
