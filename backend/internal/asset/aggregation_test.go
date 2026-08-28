package asset

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestBuildRollupPlanUsesMainMeterWithoutSubmeterDoubleCount(t *testing.T) {
	t.Parallel()
	site, area, mainID, subID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	semantic := energySemantic()
	candidates := []RollupCandidate{
		candidate(mainID, area, site, "tag:main", MeterRoleMain, semantic, []uuid.UUID{site}, nil),
		candidate(subID, area, site, "tag:sub", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil),
	}
	plan, err := BuildRollupPlan(site, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Included, []uuid.UUID{mainID}) || !reflect.DeepEqual(plan.Excluded, []uuid.UUID{subID}) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildRollupPlanSumsSubmetersWhenNoBoundaryTotalExists(t *testing.T) {
	t.Parallel()
	site, firstArea, secondArea := uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	semantic := energySemantic()
	plan, err := BuildRollupPlan(site, []RollupCandidate{
		candidate(firstID, firstArea, site, "tag:first", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil),
		candidate(secondID, secondArea, site, "tag:second", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{firstID, secondID}
	sortUUIDs(want)
	if !reflect.DeepEqual(plan.Included, want) || len(plan.Excluded) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildRollupPlanVirtualMeterReplacesDeclaredInputs(t *testing.T) {
	t.Parallel()
	site, area := uuid.New(), uuid.New()
	firstID, secondID, virtualID := uuid.New(), uuid.New(), uuid.New()
	semantic := energySemantic()
	plan, err := BuildRollupPlan(site, []RollupCandidate{
		candidate(firstID, area, site, "tag:first", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil),
		candidate(secondID, area, site, "tag:second", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil),
		candidate(virtualID, area, site, "plugin:virtual", MeterRoleVirtual, semantic, []uuid.UUID{site}, []uuid.UUID{firstID, secondID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Included, []uuid.UUID{virtualID}) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildRollupPlanHonorsExplicitExclusion(t *testing.T) {
	t.Parallel()
	site, area, includedID, excludedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	included := candidate(includedID, area, site, "tag:included", MeterRoleSubmeter, energySemantic(), []uuid.UUID{site}, nil)
	excluded := candidate(excludedID, area, site, "tag:excluded", MeterRoleSubmeter, energySemantic(), []uuid.UUID{site}, nil)
	excluded.Binding.RollupPolicy = RollupExclude
	plan, err := BuildRollupPlan(site, []RollupCandidate{included, excluded})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Included, []uuid.UUID{includedID}) || !reflect.DeepEqual(plan.Excluded, []uuid.UUID{excludedID}) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildRollupPlanRejectsUnsafeOwnershipAndConflicts(t *testing.T) {
	t.Parallel()
	site, area := uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	semantic := energySemantic()
	tests := []struct {
		name       string
		candidates []RollupCandidate
		want       error
	}{
		{"non-leaf owner", []RollupCandidate{func() RollupCandidate {
			c := candidate(firstID, area, site, "tag:a", MeterRoleDirect, semantic, []uuid.UUID{site}, nil)
			c.OwnerIsLeaf = false
			return c
		}()}, ErrInvalidAggregation},
		{"boundary outside ancestry", []RollupCandidate{candidate(firstID, area, uuid.New(), "tag:a", MeterRoleDirect, semantic, []uuid.UUID{site}, nil)}, ErrInvalidAggregation},
		{"duplicate source", []RollupCandidate{candidate(firstID, area, site, "tag:a", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil), candidate(secondID, area, site, "tag:a", MeterRoleSubmeter, semantic, []uuid.UUID{site}, nil)}, ErrDuplicateMeasurement},
		{"two main meters", []RollupCandidate{candidate(firstID, area, site, "tag:a", MeterRoleMain, semantic, []uuid.UUID{site}, nil), candidate(secondID, area, site, "tag:b", MeterRoleMain, semantic, []uuid.UUID{site}, nil)}, ErrConflictingMeters},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildRollupPlan(site, test.candidates)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
		})
	}
}

func TestBuildRollupPlanRejectsVirtualCyclesAndCrossSemanticInputs(t *testing.T) {
	t.Parallel()
	site, area, firstID, secondID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	first := candidate(firstID, area, site, "plugin:first", MeterRoleVirtual, energySemantic(), []uuid.UUID{site}, []uuid.UUID{secondID})
	second := candidate(secondID, area, site, "plugin:second", MeterRoleVirtual, energySemantic(), []uuid.UUID{site}, []uuid.UUID{firstID})
	if _, err := BuildRollupPlan(site, []RollupCandidate{first, second}); !errors.Is(err, ErrVirtualMeterCycle) {
		t.Fatalf("cycle error = %v", err)
	}
	second.Binding.VirtualInputs = nil
	second.Binding.MeterRole = MeterRoleSubmeter
	second.Binding.Semantic.Resource = ResourceThermal
	if _, err := BuildRollupPlan(site, []RollupCandidate{first, second}); !errors.Is(err, ErrInvalidAggregation) {
		t.Fatalf("semantic error = %v", err)
	}
}

func energySemantic() Semantic {
	return Semantic{Resource: ResourceElectricity, Quantity: QuantityEnergy, Unit: UnitKilowattHour, Precision: 3}
}

func candidate(id, owner, boundary uuid.UUID, source string, role MeterRole, semantic Semantic, ancestors, inputs []uuid.UUID) RollupCandidate {
	return RollupCandidate{Binding: MeasurementBinding{ID: id, OwnerAssetID: owner, BoundaryAssetID: boundary, SourceKey: source, Semantic: semantic, MeterRole: role, RollupPolicy: RollupInclude, VirtualInputs: inputs}, OwnerAncestors: ancestors, OwnerIsLeaf: true}
}
