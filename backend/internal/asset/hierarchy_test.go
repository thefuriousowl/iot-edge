package asset

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidatePlacementEnforcesSiteRoots(t *testing.T) {
	t.Parallel()

	site := KindSite
	area := KindArea
	tests := []struct {
		name       string
		kind       Kind
		parentKind *Kind
		want       error
	}{
		{name: "site root", kind: KindSite},
		{name: "area under site", kind: KindArea, parentKind: &site},
		{name: "custom under area", kind: KindCustom, parentKind: &area},
		{name: "site cannot have parent", kind: KindSite, parentKind: &site, want: ErrInvalidAssetPlacement},
		{name: "non-site requires parent", kind: KindEquipment, want: ErrInvalidAssetPlacement},
		{name: "unknown child", kind: "future", parentKind: &site, want: ErrInvalidAssetKind},
		{name: "unknown parent", kind: KindEquipment, parentKind: kindPointer("future"), want: ErrInvalidAssetKind},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePlacement(test.kind, test.parentKind)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidatePlacement() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateAncestryRejectsInvalidCyclesAndDepth(t *testing.T) {
	t.Parallel()

	assetID := uuid.New()
	validAncestors := uniqueUUIDs(MaxHierarchyDepth - 1)
	tests := []struct {
		name        string
		assetID     uuid.UUID
		ancestorIDs []uuid.UUID
		want        error
	}{
		{name: "root", assetID: assetID},
		{name: "maximum depth", assetID: assetID, ancestorIDs: validAncestors},
		{name: "missing asset ID", ancestorIDs: []uuid.UUID{uuid.New()}, want: ErrInvalidAsset},
		{name: "missing ancestor ID", assetID: assetID, ancestorIDs: []uuid.UUID{uuid.Nil}, want: ErrInvalidAsset},
		{name: "asset is its ancestor", assetID: assetID, ancestorIDs: []uuid.UUID{uuid.New(), assetID}, want: ErrHierarchyCycle},
		{name: "duplicate ancestor", assetID: assetID, ancestorIDs: []uuid.UUID{validAncestors[0], validAncestors[1], validAncestors[0]}, want: ErrHierarchyCycle},
		{name: "depth exceeded", assetID: assetID, ancestorIDs: uniqueUUIDs(MaxHierarchyDepth), want: ErrHierarchyDepth},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAncestry(test.assetID, test.ancestorIDs)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidateAncestry() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEffectiveEnabledIncludesEveryAncestor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		entity    Asset
		ancestors []Asset
		want      bool
	}{
		{name: "enabled root", entity: Asset{Enabled: true}, want: true},
		{name: "enabled path", entity: Asset{Enabled: true}, ancestors: []Asset{{Enabled: true}, {Enabled: true}}, want: true},
		{name: "disabled entity", entity: Asset{Enabled: false}, ancestors: []Asset{{Enabled: true}}},
		{name: "disabled direct parent", entity: Asset{Enabled: true}, ancestors: []Asset{{Enabled: false}, {Enabled: true}}},
		{name: "disabled distant ancestor", entity: Asset{Enabled: true}, ancestors: []Asset{{Enabled: true}, {Enabled: false}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := EffectiveEnabled(test.entity, test.ancestors); got != test.want {
				t.Errorf("EffectiveEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

func kindPointer(kind Kind) *Kind { return &kind }

func uniqueUUIDs(count int) []uuid.UUID {
	values := make([]uuid.UUID, count)
	for index := range values {
		values[index] = uuid.New()
	}
	return values
}
