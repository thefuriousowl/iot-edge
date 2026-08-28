package asset

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
)

type memoryAssetRepository struct {
	assets   map[uuid.UUID]Asset
	bindings map[uuid.UUID][]MeasurementBinding
	writes   int
}

func newMemoryAssetRepository(values ...Asset) *memoryAssetRepository {
	repository := &memoryAssetRepository{assets: map[uuid.UUID]Asset{}, bindings: map[uuid.UUID][]MeasurementBinding{}}
	for _, value := range values {
		repository.assets[value.ID] = cloneAsset(value)
	}
	return repository
}

func (repository *memoryAssetRepository) Create(_ context.Context, value *Asset) error {
	repository.assets[value.ID] = cloneAsset(*value)
	repository.writes++
	return nil
}
func (repository *memoryAssetRepository) Find(_ context.Context, id uuid.UUID) (*Asset, error) {
	value, exists := repository.assets[id]
	if !exists {
		return nil, ErrAssetNotFound
	}
	copy := cloneAsset(value)
	return &copy, nil
}
func (repository *memoryAssetRepository) List(_ context.Context, input ListInput) (*ListResult, error) {
	values := make([]Asset, 0, len(repository.assets))
	for _, value := range repository.assets {
		values = append(values, cloneAsset(value))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return &ListResult{Data: values, Page: input.Page, PerPage: input.PerPage, Total: int64(len(values)), TotalPages: 1}, nil
}
func (repository *memoryAssetRepository) ListChildren(_ context.Context, parentID *uuid.UUID) ([]Asset, error) {
	values := []Asset{}
	for _, value := range repository.assets {
		if equalUUIDPointers(value.ParentID, parentID) {
			values = append(values, cloneAsset(value))
		}
	}
	return values, nil
}
func (repository *memoryAssetRepository) Subtree(ctx context.Context, id uuid.UUID) (*TreeNode, error) {
	value, err := repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	children, _ := repository.ListChildren(ctx, &id)
	node := &TreeNode{Asset: *value, EffectiveEnabled: value.Enabled}
	for _, child := range children {
		childNode, childErr := repository.Subtree(ctx, child.ID)
		if childErr != nil {
			return nil, childErr
		}
		node.Children = append(node.Children, *childNode)
	}
	return node, nil
}
func (repository *memoryAssetRepository) Ancestors(_ context.Context, id uuid.UUID) ([]Asset, error) {
	value, exists := repository.assets[id]
	if !exists {
		return nil, ErrAssetNotFound
	}
	values := []Asset{}
	for value.ParentID != nil {
		parent, parentExists := repository.assets[*value.ParentID]
		if !parentExists {
			return nil, ErrAssetNotFound
		}
		values = append([]Asset{cloneAsset(parent)}, values...)
		value = parent
	}
	return values, nil
}
func (repository *memoryAssetRepository) Update(_ context.Context, value *Asset) error {
	repository.assets[value.ID] = cloneAsset(*value)
	repository.writes++
	return nil
}
func (repository *memoryAssetRepository) Move(_ context.Context, id uuid.UUID, parentID *uuid.UUID, position int) error {
	value := repository.assets[id]
	value.ParentID = cloneUUIDPointer(parentID)
	value.Position = position
	repository.assets[id] = value
	repository.writes++
	return nil
}
func (repository *memoryAssetRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(repository.assets, id)
	repository.writes++
	return nil
}
func (repository *memoryAssetRepository) ListBindings(_ context.Context, id uuid.UUID) ([]MeasurementBinding, error) {
	return cloneBindings(repository.bindings[id]), nil
}
func (repository *memoryAssetRepository) ListBindingsByTag(_ context.Context, tagID uuid.UUID) ([]MeasurementBinding, error) {
	values := []MeasurementBinding{}
	for _, bindings := range repository.bindings {
		for _, binding := range bindings {
			if binding.Source.Kind == SourceTag && binding.Source.TagID == tagID {
				values = append(values, cloneBinding(binding))
			}
		}
	}
	return values, nil
}
func (repository *memoryAssetRepository) ReplaceBindings(_ context.Context, id uuid.UUID, values []MeasurementBinding) error {
	repository.bindings[id] = cloneBindings(values)
	repository.writes++
	return nil
}

type memorySourceCatalog struct {
	descriptors map[string]SourceDescriptor
	err         error
}

func (catalog *memorySourceCatalog) Describe(_ context.Context, reference SourceReference) (SourceDescriptor, error) {
	if catalog.err != nil {
		return SourceDescriptor{}, catalog.err
	}
	descriptor, exists := catalog.descriptors[reference.Key()]
	if !exists {
		return SourceDescriptor{}, ErrBindingSourceMissing
	}
	return descriptor, nil
}

func TestNewServiceRejectsNilDependencies(t *testing.T) {
	repository := newMemoryAssetRepository()
	catalog := &memorySourceCatalog{}
	if _, err := NewService(nil, catalog); !errors.Is(err, ErrAssetRepositoryRequired) {
		t.Fatalf("NewService(nil repository) error = %v", err)
	}
	var typedNil *memoryAssetRepository
	if _, err := NewService(typedNil, catalog); !errors.Is(err, ErrAssetRepositoryRequired) {
		t.Fatalf("NewService(typed nil repository) error = %v", err)
	}
	if _, err := NewService(repository, nil); !errors.Is(err, ErrSourceCatalogRequired) {
		t.Fatalf("NewService(nil catalog) error = %v", err)
	}
}

func TestServiceAssetLifecycleAndDefensiveCopies(t *testing.T) {
	ctx := context.Background()
	siteID := uuid.New()
	repository := newMemoryAssetRepository(Asset{ID: siteID, Name: "Site", Kind: KindSite, Enabled: true, Metadata: Metadata(`{"seed":true}`)})
	service, err := NewService(repository, &memorySourceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	description := "  production floor  "
	timezone := "Asia/Bangkok"
	created, err := service.Create(ctx, CreateInput{ParentID: &siteID, Name: "  Floor A  ", Kind: KindArea, Description: &description, Timezone: &timezone, Metadata: Metadata(`{ "line": 1 }`)})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Floor A" || created.Description == nil || *created.Description != "production floor" || !created.Enabled || string(created.Metadata) != `{"line":1}` {
		t.Fatalf("unexpected normalized asset: %+v metadata=%s", created, created.Metadata)
	}
	created.Metadata[2] = 'X'
	*created.ParentID = uuid.New()
	stored, _ := service.Get(ctx, created.ID)
	if string(stored.Metadata) != `{"line":1}` || stored.ParentID == nil || *stored.ParentID != siteID {
		t.Fatalf("returned asset aliases repository state: %+v metadata=%s", stored, stored.Metadata)
	}

	name := " Floor B "
	clear := (*string)(nil)
	metadata := Metadata(`{"nested":{"ok":true}}`)
	updated, err := service.Update(ctx, created.ID, UpdateInput{Name: &name, Description: &clear, Timezone: &clear, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	metadata[2] = 'X'
	if updated.Name != "Floor B" || updated.Description != nil || updated.Timezone != nil || string(repository.assets[created.ID].Metadata) != `{"nested":{"ok":true}}` {
		t.Fatalf("unexpected update: %+v", updated)
	}

	if _, err := service.Move(ctx, created.ID, MoveInput{Position: 2}); !errors.Is(err, ErrInvalidAssetPlacement) {
		t.Fatalf("moving non-site to root error = %v", err)
	}
	if err := service.Delete(ctx, siteID); !errors.Is(err, ErrAssetHasDependents) {
		t.Fatalf("deleting parent error = %v", err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestServiceListDefaultsAndMoveHierarchyValidation(t *testing.T) {
	ctx := context.Background()
	siteID, areaID := uuid.New(), uuid.New()
	repository := newMemoryAssetRepository(
		Asset{ID: siteID, Name: "Site", Kind: KindSite, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: areaID, ParentID: &siteID, Name: "Area", Kind: KindArea, Enabled: true, Metadata: Metadata(`{}`)},
	)
	service, _ := NewService(repository, &memorySourceCatalog{})
	result, err := service.List(ctx, ListInput{Search: "  area  "})
	if err != nil || result.Page != 1 || result.PerPage != DefaultAssetsPerPage {
		t.Fatalf("List defaults result=%+v err=%v", result, err)
	}
	if _, err := service.List(ctx, ListInput{PerPage: MaxAssetsPerPage + 1}); !errors.Is(err, ErrInvalidAssetList) {
		t.Fatalf("oversized list error = %v", err)
	}
	if _, err := service.Move(ctx, siteID, MoveInput{ParentID: &areaID}); !errors.Is(err, ErrInvalidAssetPlacement) {
		t.Fatalf("moving site below area error = %v", err)
	}
	if _, err := service.Move(ctx, areaID, MoveInput{ParentID: &areaID}); !errors.Is(err, ErrHierarchyCycle) {
		t.Fatalf("self move error = %v", err)
	}
}

func TestServiceReplaceBindingsValidatesSourceAndCopiesValues(t *testing.T) {
	ctx := context.Background()
	siteID, meterID, tagID := uuid.New(), uuid.New(), uuid.New()
	repository := newMemoryAssetRepository(
		Asset{ID: siteID, Name: "Site", Kind: KindSite, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: meterID, ParentID: &siteID, Name: "Meter", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)},
	)
	source := TagSource(tagID)
	catalog := &memorySourceCatalog{descriptors: map[string]SourceDescriptor{source.Key(): {Reference: source, DataType: "float64", Unit: UnitKilowattHour}}}
	service, _ := NewService(repository, catalog)
	binding := MeasurementBinding{BoundaryAssetID: siteID, Source: source, Semantic: Semantic{Resource: ResourceElectricity, Quantity: QuantityEnergy, Unit: UnitKilowattHour, Precision: 3}, MeterRole: MeterRoleMain, RollupPolicy: RollupInclude}
	result, err := service.ReplaceBindings(ctx, meterID, []MeasurementBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].ID == uuid.Nil || result[0].OwnerAssetID != meterID || result[0].SourceKey != source.Key() {
		t.Fatalf("unexpected normalized binding: %+v", result)
	}
	result[0].Source.OutputKey = "mutated"
	stored := repository.bindings[meterID][0]
	if stored.Source != source {
		t.Fatalf("returned binding aliases repository state: %+v", stored.Source)
	}

	wrongReference := TagSource(uuid.New())
	catalog.descriptors[source.Key()] = SourceDescriptor{Reference: wrongReference, DataType: "float64", Unit: UnitKilowattHour}
	writes := repository.writes
	if _, err := service.ReplaceBindings(ctx, meterID, []MeasurementBinding{binding}); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("mismatched source descriptor error = %v", err)
	}
	if repository.writes != writes {
		t.Fatal("invalid replacement reached repository write")
	}

	catalog.descriptors[source.Key()] = SourceDescriptor{Reference: source, DataType: "bool", Unit: UnitKilowattHour}
	if _, err := service.ReplaceBindings(ctx, meterID, []MeasurementBinding{binding}); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("non-numeric source error = %v", err)
	}
	catalog.descriptors[source.Key()] = SourceDescriptor{Reference: source, DataType: "float64", Unit: UnitKilowattHour, DynamicUnit: true}
	if _, err := service.ReplaceBindings(ctx, meterID, []MeasurementBinding{binding}); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("dynamic unit source error = %v", err)
	}
	catalog.err = context.Canceled
	if _, err := service.ReplaceBindings(ctx, meterID, []MeasurementBinding{binding}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled source lookup error = %v", err)
	}
}

func TestServiceHierarchyRollupsPreventParentChildDoubleCounting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	siteID, areaID := uuid.New(), uuid.New()
	mainMeterID, includedSubmeterID, excludedSubmeterID, conflictingMainID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := newMemoryAssetRepository(
		Asset{ID: siteID, Name: "Site", Kind: KindSite, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: areaID, ParentID: &siteID, Name: "Area", Kind: KindArea, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: mainMeterID, ParentID: &siteID, Name: "Main meter", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: includedSubmeterID, ParentID: &areaID, Name: "Included submeter", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: excludedSubmeterID, ParentID: &areaID, Name: "Excluded submeter", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)},
		Asset{ID: conflictingMainID, ParentID: &areaID, Name: "Conflicting main", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)},
	)
	mainSource, includedSource, excludedSource, conflictingSource := TagSource(uuid.New()), TagSource(uuid.New()), TagSource(uuid.New()), TagSource(uuid.New())
	catalog := &memorySourceCatalog{descriptors: map[string]SourceDescriptor{}}
	for _, source := range []SourceReference{mainSource, includedSource, excludedSource, conflictingSource} {
		catalog.descriptors[source.Key()] = SourceDescriptor{Reference: source, DataType: "float64", Unit: UnitKilowattHour}
	}
	service, err := NewService(repository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	bind := func(source SourceReference, role MeterRole, policy RollupPolicy) MeasurementBinding {
		return MeasurementBinding{BoundaryAssetID: siteID, Source: source, Semantic: energySemantic(), MeterRole: role, RollupPolicy: policy}
	}

	included, err := service.ReplaceBindings(ctx, includedSubmeterID, []MeasurementBinding{bind(includedSource, MeterRoleSubmeter, RollupInclude)})
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := service.ReplaceBindings(ctx, excludedSubmeterID, []MeasurementBinding{bind(excludedSource, MeterRoleSubmeter, RollupExclude)})
	if err != nil {
		t.Fatal(err)
	}
	main, err := service.ReplaceBindings(ctx, mainMeterID, []MeasurementBinding{bind(mainSource, MeterRoleMain, RollupInclude)})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRollupPlan(siteID, []RollupCandidate{
		{Binding: main[0], OwnerAncestors: []uuid.UUID{siteID}, OwnerIsLeaf: true},
		{Binding: included[0], OwnerAncestors: []uuid.UUID{siteID, areaID}, OwnerIsLeaf: true},
		{Binding: excluded[0], OwnerAncestors: []uuid.UUID{siteID, areaID}, OwnerIsLeaf: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Included, []uuid.UUID{main[0].ID}) {
		t.Fatalf("main/submeter included plan = %v, want only main %s", plan.Included, main[0].ID)
	}
	wantExcluded := []uuid.UUID{included[0].ID, excluded[0].ID}
	sortUUIDs(wantExcluded)
	if !reflect.DeepEqual(plan.Excluded, wantExcluded) {
		t.Fatalf("main/submeter excluded plan = %v, want %v", plan.Excluded, wantExcluded)
	}

	writesBeforeConflict := repository.writes
	if _, err := service.ReplaceBindings(ctx, conflictingMainID, []MeasurementBinding{bind(conflictingSource, MeterRoleMain, RollupInclude)}); !errors.Is(err, ErrConflictingMeters) {
		t.Fatalf("second main meter error = %v, want %v", err, ErrConflictingMeters)
	}
	if repository.writes != writesBeforeConflict || len(repository.bindings[conflictingMainID]) != 0 {
		t.Fatal("conflicting parent/child main meter reached repository write")
	}

	if _, err := service.ReplaceBindings(ctx, areaID, []MeasurementBinding{bind(TagSource(uuid.New()), MeterRoleDirect, RollupInclude)}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("non-leaf owner error = %v, want %v", err, ErrInvalidBinding)
	}
}

func TestValidateSourceCompatibilityStateAndFixedUnit(t *testing.T) {
	source := PluginOutputSource(uuid.New(), "running")
	state := Semantic{Resource: ResourceCustom, Quantity: QuantityState, Unit: UnitBoolean}
	if err := validateSourceCompatibility(source, state, SourceDescriptor{Reference: source, DataType: "bool", Unit: UnitBoolean}); err != nil {
		t.Fatal(err)
	}
	if err := validateSourceCompatibility(source, state, SourceDescriptor{Reference: source, DataType: "float64", Unit: UnitBoolean}); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("numeric state error = %v", err)
	}
	energy := Semantic{Resource: ResourceElectricity, Quantity: QuantityEnergy, Unit: UnitKilowattHour}
	if err := validateSourceCompatibility(source, energy, SourceDescriptor{Reference: source, DataType: "float64", Unit: UnitWattHour}); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("fixed unit mismatch error = %v", err)
	}
}

func TestServiceReplaceBindingsRejectsUnboundedMeasurementSet(t *testing.T) {
	repository := newMemoryAssetRepository()
	service, _ := NewService(repository, &memorySourceCatalog{})
	values := make([]MeasurementBinding, MaxMeasurementsPerAsset+1)
	if _, err := service.ReplaceBindings(context.Background(), uuid.New(), values); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("oversized replacement error = %v", err)
	}
	if repository.writes != 0 {
		t.Fatal("oversized replacement reached repository")
	}
}

func equalUUIDPointers(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
