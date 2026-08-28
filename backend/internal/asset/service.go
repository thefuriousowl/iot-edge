package asset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxAssetNameLength        = 100
	maxAssetDescriptionLength = 2000
	maxAssetTimezoneLength    = 100
	maxAssetSearchLength      = 100
)

var (
	ErrAssetRepositoryRequired = errors.New("asset repository is required")
	ErrSourceCatalogRequired   = errors.New("asset measurement source catalog is required")
	ErrInvalidAssetInput       = errors.New("invalid asset input")
	ErrInvalidMoveInput        = errors.New("invalid asset move input")
	ErrIncompatibleSource      = errors.New("measurement source is incompatible with semantic")
)

type SourceDescriptor struct {
	Reference   SourceReference
	DataType    string
	Unit        Unit
	DynamicUnit bool
}

type SourceCatalog interface {
	Describe(context.Context, SourceReference) (SourceDescriptor, error)
}

type CreateInput struct {
	ParentID    *uuid.UUID
	Name        string
	Kind        Kind
	Description *string
	Enabled     *bool
	Timezone    *string
	Position    int
	Metadata    Metadata
}

type UpdateInput struct {
	Name        *string
	Kind        *Kind
	Description **string
	Enabled     *bool
	Timezone    **string
	Position    *int
	Metadata    Metadata
}

type MoveInput struct {
	ParentID *uuid.UUID
	Position int
}

type Service struct {
	repository Repository
	sources    SourceCatalog
}

func NewService(repository Repository, sources SourceCatalog) (*Service, error) {
	if isNilServiceDependency(repository) {
		return nil, ErrAssetRepositoryRequired
	}
	if isNilServiceDependency(sources) {
		return nil, ErrSourceCatalogRequired
	}
	return &Service{repository: repository, sources: sources}, nil
}

func (service *Service) Create(ctx context.Context, input CreateInput) (*Asset, error) {
	if ctx == nil {
		return nil, ErrInvalidAssetInput
	}
	entity, err := normalizeCreateInput(input)
	if err != nil {
		return nil, err
	}
	var parentKind *Kind
	ancestors := []uuid.UUID{}
	if entity.ParentID != nil {
		parent, findErr := service.repository.Find(ctx, *entity.ParentID)
		if findErr != nil {
			return nil, findErr
		}
		parentKind = &parent.Kind
		lineage, lineageErr := service.repository.Ancestors(ctx, parent.ID)
		if lineageErr != nil {
			return nil, lineageErr
		}
		for _, ancestor := range lineage {
			ancestors = append(ancestors, ancestor.ID)
		}
		ancestors = append(ancestors, parent.ID)
	}
	if err := ValidatePlacement(entity.Kind, parentKind); err != nil {
		return nil, err
	}
	if err := ValidateAncestry(entity.ID, ancestors); err != nil {
		return nil, err
	}
	if err := service.repository.Create(ctx, entity); err != nil {
		return nil, err
	}
	return service.Get(ctx, entity.ID)
}

func (service *Service) Get(ctx context.Context, id uuid.UUID) (*Asset, error) {
	if ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidAssetInput
	}
	entity, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	cloned := cloneAsset(*entity)
	return &cloned, nil
}

func (service *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if ctx == nil {
		return nil, ErrInvalidAssetList
	}
	input.Search = strings.TrimSpace(input.Search)
	if len(input.Search) > maxAssetSearchLength {
		return nil, ErrInvalidAssetList
	}
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = DefaultAssetsPerPage
	}
	if input.PerPage > MaxAssetsPerPage || input.Kind != nil && !input.Kind.Valid() {
		return nil, ErrInvalidAssetList
	}
	result, err := service.repository.List(ctx, input)
	if err != nil {
		return nil, err
	}
	return cloneListResult(result), nil
}

func (service *Service) Children(ctx context.Context, parentID *uuid.UUID) ([]Asset, error) {
	if ctx == nil {
		return nil, ErrInvalidAssetInput
	}
	if parentID != nil && *parentID == uuid.Nil {
		return nil, ErrInvalidAssetInput
	}
	values, err := service.repository.ListChildren(ctx, parentID)
	if err != nil {
		return nil, err
	}
	return cloneAssets(values), nil
}
func (service *Service) Subtree(ctx context.Context, id uuid.UUID) (*TreeNode, error) {
	if ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidAssetInput
	}
	node, err := service.repository.Subtree(ctx, id)
	if err != nil {
		return nil, err
	}
	cloned := cloneTree(*node)
	return &cloned, nil
}
func (service *Service) Ancestors(ctx context.Context, id uuid.UUID) ([]Asset, error) {
	if ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidAssetInput
	}
	values, err := service.repository.Ancestors(ctx, id)
	if err != nil {
		return nil, err
	}
	return cloneAssets(values), nil
}

func (service *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*Asset, error) {
	if ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidAssetInput
	}
	current, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	entity := cloneAsset(*current)
	if input.Name != nil {
		entity.Name = strings.TrimSpace(*input.Name)
	}
	if input.Kind != nil {
		entity.Kind = *input.Kind
	}
	if input.Description != nil {
		entity.Description = normalizeOptionalText(*input.Description)
	}
	if input.Enabled != nil {
		entity.Enabled = *input.Enabled
	}
	if input.Timezone != nil {
		entity.Timezone = cloneStringPointer(*input.Timezone)
	}
	if input.Position != nil {
		entity.Position = *input.Position
	}
	if input.Metadata != nil {
		entity.Metadata = append(Metadata(nil), input.Metadata...)
	}
	if err := normalizeAsset(&entity); err != nil {
		return nil, err
	}
	var parentKind *Kind
	if entity.ParentID != nil {
		parent, findErr := service.repository.Find(ctx, *entity.ParentID)
		if findErr != nil {
			return nil, findErr
		}
		parentKind = &parent.Kind
	}
	if err := ValidatePlacement(entity.Kind, parentKind); err != nil {
		return nil, err
	}
	if err := service.repository.Update(ctx, &entity); err != nil {
		return nil, err
	}
	return service.Get(ctx, id)
}

func (service *Service) Move(ctx context.Context, id uuid.UUID, input MoveInput) (*Asset, error) {
	if ctx == nil || id == uuid.Nil || input.Position < 0 || input.ParentID != nil && *input.ParentID == uuid.Nil {
		return nil, ErrInvalidMoveInput
	}
	entity, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	var parentKind *Kind
	ancestorIDs := []uuid.UUID{}
	if input.ParentID != nil {
		parent, findErr := service.repository.Find(ctx, *input.ParentID)
		if findErr != nil {
			return nil, findErr
		}
		parentKind = &parent.Kind
		lineage, lineageErr := service.repository.Ancestors(ctx, parent.ID)
		if lineageErr != nil {
			return nil, lineageErr
		}
		for _, ancestor := range lineage {
			ancestorIDs = append(ancestorIDs, ancestor.ID)
		}
		ancestorIDs = append(ancestorIDs, parent.ID)
	}
	if err := ValidatePlacement(entity.Kind, parentKind); err != nil {
		return nil, err
	}
	if err := ValidateAncestry(entity.ID, ancestorIDs); err != nil {
		return nil, err
	}
	if err := service.repository.Move(ctx, id, input.ParentID, input.Position); err != nil {
		return nil, err
	}
	return service.Get(ctx, id)
}

func (service *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if ctx == nil || id == uuid.Nil {
		return ErrInvalidAssetInput
	}
	if _, err := service.repository.Find(ctx, id); err != nil {
		return err
	}
	children, err := service.repository.ListChildren(ctx, &id)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return ErrAssetHasDependents
	}
	bindings, err := service.repository.ListBindings(ctx, id)
	if err != nil {
		return err
	}
	if len(bindings) > 0 {
		return ErrAssetHasDependents
	}
	return service.repository.Delete(ctx, id)
}

func (service *Service) Bindings(ctx context.Context, assetID uuid.UUID) ([]MeasurementBinding, error) {
	if ctx == nil || assetID == uuid.Nil {
		return nil, ErrInvalidBinding
	}
	values, err := service.repository.ListBindings(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return cloneBindings(values), nil
}

func (service *Service) ReplaceBindings(ctx context.Context, assetID uuid.UUID, bindings []MeasurementBinding) ([]MeasurementBinding, error) {
	if ctx == nil || assetID == uuid.Nil || len(bindings) > MaxMeasurementsPerAsset {
		return nil, ErrInvalidBinding
	}
	if _, err := service.repository.Find(ctx, assetID); err != nil {
		return nil, err
	}
	children, err := service.repository.ListChildren(ctx, &assetID)
	if err != nil {
		return nil, err
	}
	if len(children) > 0 {
		return nil, ErrInvalidBinding
	}
	ancestors, err := service.repository.Ancestors(ctx, assetID)
	if err != nil {
		return nil, err
	}
	ancestorIDs := make([]uuid.UUID, 0, len(ancestors))
	for _, ancestor := range ancestors {
		ancestorIDs = append(ancestorIDs, ancestor.ID)
	}
	normalized := make([]MeasurementBinding, len(bindings))
	seenIDs := map[uuid.UUID]struct{}{}
	seenSources := map[string]struct{}{}
	for index, binding := range bindings {
		binding = cloneBinding(binding)
		if binding.ID == uuid.Nil {
			binding.ID = uuid.New()
		}
		binding.OwnerAssetID = assetID
		binding.SourceKey = binding.Source.Key()
		if _, exists := seenIDs[binding.ID]; exists {
			return nil, ErrInvalidBinding
		}
		seenIDs[binding.ID] = struct{}{}
		if binding.Source.Validate() != nil || binding.Semantic.Validate() != nil {
			return nil, ErrInvalidBinding
		}
		if _, exists := seenSources[binding.SourceKey]; exists {
			return nil, ErrDuplicateMeasurement
		}
		seenSources[binding.SourceKey] = struct{}{}
		if binding.BoundaryAssetID != assetID && !containsAsset(ancestorIDs, binding.BoundaryAssetID) {
			return nil, ErrInvalidAggregation
		}
		descriptor, describeErr := service.sources.Describe(ctx, binding.Source)
		if describeErr != nil {
			return nil, describeErr
		}
		if err := validateSourceCompatibility(binding.Source, binding.Semantic, descriptor); err != nil {
			return nil, err
		}
		normalized[index] = binding
	}
	if err := validateSubmittedBindings(assetID, ancestorIDs, normalized); err != nil {
		return nil, err
	}
	if err := service.validateHierarchyRollups(ctx, assetID, normalized); err != nil {
		return nil, err
	}
	if err := service.repository.ReplaceBindings(ctx, assetID, normalized); err != nil {
		return nil, err
	}
	return service.Bindings(ctx, assetID)
}

func (service *Service) validateHierarchyRollups(ctx context.Context, ownerID uuid.UUID, replacement []MeasurementBinding) error {
	boundaries := map[uuid.UUID]struct{}{}
	for _, binding := range replacement {
		boundaries[binding.BoundaryAssetID] = struct{}{}
	}
	for boundaryID := range boundaries {
		tree, err := service.repository.Subtree(ctx, boundaryID)
		if err != nil {
			return err
		}
		candidates := []RollupCandidate{}
		var walk func(TreeNode) error
		walk = func(node TreeNode) error {
			values := replacement
			if node.ID != ownerID {
				var listErr error
				values, listErr = service.repository.ListBindings(ctx, node.ID)
				if listErr != nil {
					return listErr
				}
			}
			for _, binding := range values {
				if binding.BoundaryAssetID == boundaryID {
					candidates = append(candidates, RollupCandidate{Binding: binding, OwnerAncestors: []uuid.UUID{boundaryID}, OwnerIsLeaf: len(node.Children) == 0})
				}
			}
			for _, child := range node.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(*tree); err != nil {
			return err
		}
		if _, err := BuildRollupPlan(boundaryID, candidates); err != nil {
			return err
		}
	}
	return nil
}

func normalizeCreateInput(input CreateInput) (*Asset, error) {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	entity := &Asset{ID: uuid.New(), ParentID: cloneUUIDPointer(input.ParentID), Name: input.Name, Kind: input.Kind, Description: cloneStringPointer(input.Description), Enabled: enabled, Timezone: cloneStringPointer(input.Timezone), Position: input.Position, Metadata: append(Metadata(nil), input.Metadata...)}
	if len(entity.Metadata) == 0 {
		entity.Metadata = Metadata(`{}`)
	}
	if err := normalizeAsset(entity); err != nil {
		return nil, err
	}
	return entity, nil
}
func normalizeAsset(entity *Asset) error {
	entity.Name = strings.TrimSpace(entity.Name)
	if entity.Name == "" || len(entity.Name) > maxAssetNameLength || !entity.Kind.Valid() || entity.Position < 0 {
		return ErrInvalidAssetInput
	}
	entity.Description = normalizeOptionalText(entity.Description)
	if entity.Description != nil && len(*entity.Description) > maxAssetDescriptionLength {
		return ErrInvalidAssetInput
	}
	if entity.Timezone != nil {
		value := strings.TrimSpace(*entity.Timezone)
		if value == "" || value != *entity.Timezone || len(value) > maxAssetTimezoneLength {
			return ErrInvalidAssetInput
		}
		location, err := time.LoadLocation(value)
		if err != nil || location.String() != value {
			return ErrInvalidAssetInput
		}
	}
	metadata, err := normalizeMetadata(entity.Metadata)
	if err != nil {
		return err
	}
	entity.Metadata = metadata
	return nil
}
func normalizeMetadata(raw Metadata) (Metadata, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Metadata(`{}`), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, ErrInvalidAssetInput
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, ErrInvalidAssetInput
	}
	return Metadata(canonical), nil
}
func validateSourceCompatibility(source SourceReference, semantic Semantic, descriptor SourceDescriptor) error {
	if descriptor.Reference != source {
		return ErrIncompatibleSource
	}
	if descriptor.DynamicUnit {
		return ErrIncompatibleSource
	}
	if descriptor.Unit != "" && descriptor.Unit != semantic.Unit {
		return ErrIncompatibleSource
	}
	if semantic.Quantity == QuantityState {
		if descriptor.DataType != "bool" {
			return ErrIncompatibleSource
		}
		return nil
	}
	switch descriptor.DataType {
	case "int16", "uint16", "int32", "uint32", "float32", "float64":
		return nil
	default:
		return ErrIncompatibleSource
	}
}
func validateSubmittedBindings(assetID uuid.UUID, ancestors []uuid.UUID, bindings []MeasurementBinding) error {
	ids := map[uuid.UUID]struct{}{}
	for _, binding := range bindings {
		ids[binding.ID] = struct{}{}
	}
	for _, binding := range bindings {
		for _, inputID := range binding.VirtualInputs {
			if _, exists := ids[inputID]; !exists {
				return ErrInvalidBinding
			}
		}
	}
	candidates := make([]RollupCandidate, 0, len(bindings))
	for _, binding := range bindings {
		candidates = append(candidates, RollupCandidate{Binding: binding, OwnerAncestors: ancestors, OwnerIsLeaf: true})
	}
	_, err := BuildRollupPlan(assetID, candidates)
	return err
}

func cloneAsset(value Asset) Asset {
	value.ParentID = cloneUUIDPointer(value.ParentID)
	value.Description = cloneStringPointer(value.Description)
	value.Timezone = cloneStringPointer(value.Timezone)
	value.Metadata = append(Metadata(nil), value.Metadata...)
	return value
}
func cloneAssets(values []Asset) []Asset {
	result := make([]Asset, len(values))
	for index, value := range values {
		result[index] = cloneAsset(value)
	}
	return result
}
func cloneListResult(value *ListResult) *ListResult {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Data = cloneAssets(value.Data)
	return &cloned
}
func cloneTree(value TreeNode) TreeNode {
	value.Asset = cloneAsset(value.Asset)
	value.Children = append([]TreeNode(nil), value.Children...)
	for index := range value.Children {
		value.Children[index] = cloneTree(value.Children[index])
	}
	return value
}
func cloneBinding(value MeasurementBinding) MeasurementBinding {
	value.Semantic.Reference.TemperatureKelvin = cloneFloatPointer(value.Semantic.Reference.TemperatureKelvin)
	value.Semantic.Reference.PressurePascal = cloneFloatPointer(value.Semantic.Reference.PressurePascal)
	value.VirtualInputs = append([]uuid.UUID(nil), value.VirtualInputs...)
	return value
}
func cloneBindings(values []MeasurementBinding) []MeasurementBinding {
	result := make([]MeasurementBinding, len(values))
	for index, value := range values {
		result[index] = cloneBinding(value)
	}
	return result
}
func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func cloneFloatPointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func normalizeOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}
func isNilServiceDependency(value any) bool {
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
