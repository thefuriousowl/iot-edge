package asset

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrInvalidAsset          = errors.New("invalid asset")
	ErrInvalidAssetKind      = errors.New("invalid asset kind")
	ErrInvalidAssetPlacement = errors.New("invalid asset placement")
	ErrHierarchyCycle        = errors.New("asset hierarchy cycle")
	ErrHierarchyDepth        = errors.New("asset hierarchy depth exceeded")
)

func ValidatePlacement(kind Kind, parentKind *Kind) error {
	if !kind.Valid() {
		return ErrInvalidAssetKind
	}
	if parentKind != nil && !parentKind.Valid() {
		return ErrInvalidAssetKind
	}
	if kind == KindSite {
		if parentKind != nil {
			return ErrInvalidAssetPlacement
		}
		return nil
	}
	if parentKind == nil {
		return ErrInvalidAssetPlacement
	}
	return nil
}

func ValidateAncestry(assetID uuid.UUID, ancestorIDs []uuid.UUID) error {
	if assetID == uuid.Nil {
		return ErrInvalidAsset
	}
	if len(ancestorIDs)+1 > MaxHierarchyDepth {
		return ErrHierarchyDepth
	}
	seen := make(map[uuid.UUID]struct{}, len(ancestorIDs)+1)
	seen[assetID] = struct{}{}
	for _, ancestorID := range ancestorIDs {
		if ancestorID == uuid.Nil {
			return ErrInvalidAsset
		}
		if _, exists := seen[ancestorID]; exists {
			return ErrHierarchyCycle
		}
		seen[ancestorID] = struct{}{}
	}
	return nil
}

func EffectiveEnabled(entity Asset, ancestors []Asset) bool {
	if !entity.Enabled {
		return false
	}
	for _, ancestor := range ancestors {
		if !ancestor.Enabled {
			return false
		}
	}
	return true
}
