package asset

import (
	"errors"
	"sort"
	"strconv"

	"github.com/google/uuid"
)

type MeterRole string
type RollupPolicy string
type SourceKind string

const (
	MeterRoleDirect   MeterRole = "direct"
	MeterRoleMain     MeterRole = "main"
	MeterRoleSubmeter MeterRole = "submeter"
	MeterRoleVirtual  MeterRole = "virtual"

	RollupInclude RollupPolicy = "include"
	RollupExclude RollupPolicy = "exclude"

	SourceTag          SourceKind = "tag"
	SourcePluginOutput SourceKind = "plugin_output"
)

var (
	ErrInvalidAggregation   = errors.New("invalid aggregation boundary")
	ErrDuplicateMeasurement = errors.New("duplicate measurement source")
	ErrConflictingMeters    = errors.New("conflicting meters in aggregation boundary")
	ErrVirtualMeterCycle    = errors.New("virtual meter dependency cycle")
)

type MeasurementBinding struct {
	ID              uuid.UUID       `json:"id"`
	OwnerAssetID    uuid.UUID       `json:"owner_asset_id"`
	BoundaryAssetID uuid.UUID       `json:"boundary_asset_id"`
	SourceKey       string          `json:"source_key"`
	Source          SourceReference `json:"source"`
	Semantic        Semantic        `json:"semantic"`
	MeterRole       MeterRole       `json:"meter_role"`
	RollupPolicy    RollupPolicy    `json:"rollup_policy"`
	VirtualInputs   []uuid.UUID     `json:"virtual_inputs,omitempty"`
}

type SourceReference struct {
	Kind             SourceKind `json:"kind"`
	TagID            uuid.UUID  `json:"tag_id,omitempty"`
	PluginInstanceID uuid.UUID  `json:"plugin_instance_id,omitempty"`
	OutputKey        string     `json:"output_key,omitempty"`
}

func TagSource(tagID uuid.UUID) SourceReference {
	return SourceReference{Kind: SourceTag, TagID: tagID}
}
func PluginOutputSource(instanceID uuid.UUID, outputKey string) SourceReference {
	return SourceReference{Kind: SourcePluginOutput, PluginInstanceID: instanceID, OutputKey: outputKey}
}

func (source SourceReference) Validate() error {
	switch source.Kind {
	case SourceTag:
		if source.TagID == uuid.Nil || source.PluginInstanceID != uuid.Nil || source.OutputKey != "" {
			return ErrInvalidBinding
		}
	case SourcePluginOutput:
		if source.TagID != uuid.Nil || source.PluginInstanceID == uuid.Nil || source.OutputKey == "" {
			return ErrInvalidBinding
		}
	default:
		return ErrInvalidBinding
	}
	return nil
}

func (source SourceReference) Key() string {
	if source.Kind == SourceTag {
		return "tag:" + source.TagID.String()
	}
	if source.Kind == SourcePluginOutput {
		return "plugin_output:" + source.PluginInstanceID.String() + ":" + source.OutputKey
	}
	return ""
}

type RollupCandidate struct {
	Binding        MeasurementBinding
	OwnerAncestors []uuid.UUID
	OwnerIsLeaf    bool
}

type RollupPlan struct {
	Included []uuid.UUID `json:"included"`
	Excluded []uuid.UUID `json:"excluded"`
}

func (candidate RollupCandidate) Validate() error {
	binding := candidate.Binding
	if binding.ID == uuid.Nil || binding.OwnerAssetID == uuid.Nil || binding.BoundaryAssetID == uuid.Nil || binding.sourceKey() == "" || !candidate.OwnerIsLeaf || binding.Semantic.Validate() != nil {
		return ErrInvalidAggregation
	}
	if !containsAsset(candidate.OwnerAncestors, binding.BoundaryAssetID) && binding.OwnerAssetID != binding.BoundaryAssetID {
		return ErrInvalidAggregation
	}
	switch binding.MeterRole {
	case MeterRoleDirect, MeterRoleMain, MeterRoleSubmeter:
		if len(binding.VirtualInputs) != 0 {
			return ErrInvalidAggregation
		}
	case MeterRoleVirtual:
		if len(binding.VirtualInputs) == 0 {
			return ErrInvalidAggregation
		}
	default:
		return ErrInvalidAggregation
	}
	if binding.RollupPolicy != RollupInclude && binding.RollupPolicy != RollupExclude {
		return ErrInvalidAggregation
	}
	return nil
}

func (binding MeasurementBinding) sourceKey() string {
	if binding.Source.Validate() == nil {
		return binding.Source.Key()
	}
	return binding.SourceKey
}

func BuildRollupPlan(targetAssetID uuid.UUID, candidates []RollupCandidate) (RollupPlan, error) {
	if targetAssetID == uuid.Nil {
		return RollupPlan{}, ErrInvalidAggregation
	}
	byID := make(map[uuid.UUID]RollupCandidate, len(candidates))
	bySource := make(map[string]uuid.UUID, len(candidates))
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return RollupPlan{}, err
		}
		if candidate.Binding.OwnerAssetID != targetAssetID && !containsAsset(candidate.OwnerAncestors, targetAssetID) {
			return RollupPlan{}, ErrInvalidAggregation
		}
		if _, exists := byID[candidate.Binding.ID]; exists {
			return RollupPlan{}, ErrDuplicateMeasurement
		}
		sourceKey := candidate.Binding.sourceKey()
		if _, exists := bySource[sourceKey]; exists {
			return RollupPlan{}, ErrDuplicateMeasurement
		}
		byID[candidate.Binding.ID] = candidate
		bySource[sourceKey] = candidate.Binding.ID
	}
	if virtualDependencyCycle(byID) {
		return RollupPlan{}, ErrVirtualMeterCycle
	}

	included := make(map[uuid.UUID]bool, len(candidates))
	for id, candidate := range byID {
		included[id] = candidate.Binding.RollupPolicy == RollupInclude
	}

	// A main meter defines the total at its explicit boundary. Its downstream
	// direct/submeters are diagnostic contributors and must not be added again.
	mainByGroup := make(map[string]uuid.UUID)
	for id, candidate := range byID {
		if !included[id] || candidate.Binding.MeterRole != MeterRoleMain {
			continue
		}
		group := aggregationGroup(candidate.Binding)
		if _, exists := mainByGroup[group]; exists {
			return RollupPlan{}, ErrConflictingMeters
		}
		mainByGroup[group] = id
	}
	for id, candidate := range byID {
		mainID, exists := mainByGroup[aggregationGroup(candidate.Binding)]
		if exists && id != mainID {
			included[id] = false
		}
	}

	// A selected virtual meter replaces exactly its declared inputs. Nested
	// virtual meters are supported after cycle validation.
	for id, candidate := range byID {
		if !included[id] || candidate.Binding.MeterRole != MeterRoleVirtual {
			continue
		}
		for _, inputID := range candidate.Binding.VirtualInputs {
			input, exists := byID[inputID]
			if !exists || aggregationGroup(input.Binding) != aggregationGroup(candidate.Binding) {
				return RollupPlan{}, ErrInvalidAggregation
			}
			included[inputID] = false
		}
	}

	plan := RollupPlan{Included: make([]uuid.UUID, 0), Excluded: make([]uuid.UUID, 0)}
	for id := range byID {
		if included[id] {
			plan.Included = append(plan.Included, id)
		} else {
			plan.Excluded = append(plan.Excluded, id)
		}
	}
	sortUUIDs(plan.Included)
	sortUUIDs(plan.Excluded)
	return plan, nil
}

func aggregationGroup(binding MeasurementBinding) string {
	canonical, _ := binding.Semantic.CanonicalUnit()
	return binding.BoundaryAssetID.String() + "\x00" + string(binding.Semantic.Resource) + "\x00" + string(binding.Semantic.Quantity) + "\x00" + string(canonical) + "\x00" + referenceKey(binding.Semantic.Reference)
}

func referenceKey(reference ReferenceCondition) string {
	temperature, pressure := 0.0, 0.0
	if reference.TemperatureKelvin != nil {
		temperature = *reference.TemperatureKelvin
	}
	if reference.PressurePascal != nil {
		pressure = *reference.PressurePascal
	}
	return string(reference.VolumeBasis) + "\x00" + string(reference.PressureBasis) + "\x00" + formatFloat(temperature) + "\x00" + formatFloat(pressure)
}

func virtualDependencyCycle(candidates map[uuid.UUID]RollupCandidate) bool {
	state := make(map[uuid.UUID]uint8, len(candidates))
	var visit func(uuid.UUID) bool
	visit = func(id uuid.UUID) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, dependency := range candidates[id].Binding.VirtualInputs {
			if _, exists := candidates[dependency]; exists && visit(dependency) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range candidates {
		if visit(id) {
			return true
		}
	}
	return false
}

func containsAsset(ids []uuid.UUID, wanted uuid.UUID) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}
func sortUUIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
}
func formatFloat(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
