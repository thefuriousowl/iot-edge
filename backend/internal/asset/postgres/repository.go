package assetpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) asset.Repository { return &repository{db: db} }

func (repository *repository) Create(ctx context.Context, entity *asset.Asset) error {
	if entity == nil {
		return asset.ErrInvalidAsset
	}
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	if len(entity.Metadata) == 0 {
		entity.Metadata = asset.Metadata(`{}`)
	}
	return mapError(repository.db.WithContext(ctx).Create(entity).Error)
}

func (repository *repository) Find(ctx context.Context, id uuid.UUID) (*asset.Asset, error) {
	if id == uuid.Nil {
		return nil, asset.ErrInvalidAsset
	}
	var entity asset.Asset
	if err := repository.db.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		return nil, mapError(err)
	}
	return &entity, nil
}

func (repository *repository) List(ctx context.Context, input asset.ListInput) (*asset.ListResult, error) {
	input.Search = strings.TrimSpace(input.Search)
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = asset.DefaultAssetsPerPage
	}
	if input.PerPage > asset.MaxAssetsPerPage || input.Kind != nil && !input.Kind.IsValid() {
		return nil, asset.ErrInvalidAssetList
	}
	query := repository.db.WithContext(ctx).Model(&asset.Asset{})
	if input.Kind != nil {
		query = query.Where("kind = ?", *input.Kind)
	}
	if input.Enabled != nil {
		query = query.Where("enabled = ?", *input.Enabled)
	}
	if input.Search != "" {
		query = query.Where(`LOWER(name) LIKE ? ESCAPE '\'`, "%"+escapeLike(strings.ToLower(input.Search))+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, mapError(err)
	}
	entities := make([]asset.Asset, 0)
	if err := query.Order("position ASC, LOWER(name) ASC, id ASC").Offset((input.Page - 1) * input.PerPage).Limit(input.PerPage).Find(&entities).Error; err != nil {
		return nil, mapError(err)
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &asset.ListResult{Data: entities, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}, nil
}

func (repository *repository) ListChildren(ctx context.Context, parentID *uuid.UUID) ([]asset.Asset, error) {
	query := repository.db.WithContext(ctx)
	if parentID == nil {
		query = query.Where("parent_id IS NULL")
	} else {
		query = query.Where("parent_id = ?", *parentID)
	}
	entities := make([]asset.Asset, 0)
	err := query.Order("position ASC, LOWER(name) ASC, id ASC").Find(&entities).Error
	return entities, mapError(err)
}

type subtreeRow struct {
	asset.Asset
	Depth            int   `gorm:"column:depth"`
	EffectiveEnabled bool  `gorm:"column:effective_enabled"`
	MeasurementCount int64 `gorm:"column:measurement_count"`
}

func (repository *repository) Subtree(ctx context.Context, rootID uuid.UUID) (*asset.TreeNode, error) {
	if rootID == uuid.Nil {
		return nil, asset.ErrInvalidAsset
	}
	rows := make([]subtreeRow, 0)
	err := repository.db.WithContext(ctx).Raw(`
		WITH RECURSIVE tree AS (
			SELECT assets.*, 0 AS depth, assets.enabled AS effective_enabled FROM assets WHERE id = ?
			UNION ALL
			SELECT child.*, tree.depth + 1, tree.effective_enabled AND child.enabled
			FROM assets child JOIN tree ON child.parent_id = tree.id
		)
		SELECT tree.*, (SELECT COUNT(*) FROM measurement_bindings WHERE asset_id = tree.id) AS measurement_count
		FROM tree
	`, rootID).Scan(&rows).Error
	if err != nil {
		return nil, mapError(err)
	}
	if len(rows) == 0 {
		return nil, asset.ErrAssetNotFound
	}
	byID := make(map[uuid.UUID]subtreeRow, len(rows))
	children := make(map[uuid.UUID][]uuid.UUID, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
		if row.ParentID != nil {
			children[*row.ParentID] = append(children[*row.ParentID], row.ID)
		}
	}
	for parentID := range children {
		sort.Slice(children[parentID], func(i, j int) bool {
			return assetLess(byID[children[parentID][i]].Asset, byID[children[parentID][j]].Asset)
		})
	}
	var build func(uuid.UUID) asset.TreeNode
	build = func(id uuid.UUID) asset.TreeNode {
		row := byID[id]
		node := asset.TreeNode{Asset: row.Asset, Depth: row.Depth, EffectiveEnabled: row.EffectiveEnabled, MeasurementCount: row.MeasurementCount, Children: make([]asset.TreeNode, 0, len(children[id]))}
		for _, childID := range children[id] {
			node.Children = append(node.Children, build(childID))
		}
		return node
	}
	root := build(rootID)
	return &root, nil
}

func (repository *repository) Ancestors(ctx context.Context, id uuid.UUID) ([]asset.Asset, error) {
	if _, err := repository.Find(ctx, id); err != nil {
		return nil, err
	}
	entities := make([]asset.Asset, 0)
	err := repository.db.WithContext(ctx).Raw(`
		WITH RECURSIVE lineage AS (
			SELECT parent.*, 1 AS distance FROM assets node JOIN assets parent ON parent.id = node.parent_id WHERE node.id = ?
			UNION ALL
			SELECT parent.*, lineage.distance + 1 FROM lineage JOIN assets parent ON parent.id = lineage.parent_id
		)
		SELECT id,parent_id,name,kind,description,enabled,timezone,position,metadata,created_at,updated_at
		FROM lineage ORDER BY distance DESC
	`, id).Scan(&entities).Error
	return entities, mapError(err)
}

func (repository *repository) Update(ctx context.Context, entity *asset.Asset) error {
	if entity == nil || entity.ID == uuid.Nil {
		return asset.ErrInvalidAsset
	}
	if len(entity.Metadata) == 0 {
		entity.Metadata = asset.Metadata(`{}`)
	}
	result := repository.db.WithContext(ctx).Model(&asset.Asset{}).Where("id = ?", entity.ID).Updates(map[string]any{
		"name": entity.Name, "kind": entity.Kind, "description": entity.Description, "enabled": entity.Enabled, "timezone": entity.Timezone, "position": entity.Position, "metadata": entity.Metadata, "updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
	})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return asset.ErrAssetNotFound
	}
	return nil
}

func (repository *repository) Move(ctx context.Context, id uuid.UUID, parentID *uuid.UUID, position int) error {
	if id == uuid.Nil || position < 0 {
		return asset.ErrInvalidAsset
	}
	result := repository.db.WithContext(ctx).Model(&asset.Asset{}).Where("id = ?", id).Updates(map[string]any{"parent_id": parentID, "position": position, "updated_at": gorm.Expr("CURRENT_TIMESTAMP")})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return asset.ErrAssetNotFound
	}
	return nil
}

func (repository *repository) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return asset.ErrInvalidAsset
	}
	result := repository.db.WithContext(ctx).Delete(&asset.Asset{}, "id = ?", id)
	if result.Error != nil {
		return mapDeleteError(result.Error)
	}
	if result.RowsAffected == 0 {
		return asset.ErrAssetNotFound
	}
	return nil
}

type bindingRow struct {
	ID               uuid.UUID          `gorm:"column:id"`
	AssetID          uuid.UUID          `gorm:"column:asset_id"`
	BoundaryAssetID  uuid.UUID          `gorm:"column:boundary_asset_id"`
	SourceType       asset.SourceKind   `gorm:"column:source_type"`
	TagID            *uuid.UUID         `gorm:"column:tag_id"`
	PluginInstanceID *uuid.UUID         `gorm:"column:plugin_instance_id"`
	OutputKey        *string            `gorm:"column:output_key"`
	Resource         asset.Resource     `gorm:"column:resource"`
	Quantity         asset.Quantity     `gorm:"column:quantity"`
	Unit             asset.Unit         `gorm:"column:unit"`
	Precision        uint8              `gorm:"column:precision"`
	Reference        json.RawMessage    `gorm:"column:reference"`
	MeterRole        asset.MeterRole    `gorm:"column:meter_role"`
	RollupPolicy     asset.RollupPolicy `gorm:"column:rollup_policy"`
}

func (repository *repository) ListBindings(ctx context.Context, assetID uuid.UUID) ([]asset.MeasurementBinding, error) {
	if _, err := repository.Find(ctx, assetID); err != nil {
		return nil, err
	}
	rows := make([]bindingRow, 0)
	if err := repository.db.WithContext(ctx).Table("measurement_bindings").Where("asset_id = ?", assetID).Order("created_at ASC, id ASC").Scan(&rows).Error; err != nil {
		return nil, mapError(err)
	}
	return loadBindings(ctx, repository.db, rows)
}

func (repository *repository) ListBindingsByTag(ctx context.Context, tagID uuid.UUID) ([]asset.MeasurementBinding, error) {
	if tagID == uuid.Nil {
		return nil, asset.ErrInvalidBinding
	}
	rows := make([]bindingRow, 0)
	if err := repository.db.WithContext(ctx).Table("measurement_bindings").Where("source_type = ? AND tag_id = ?", asset.SourceTag, tagID).Order("created_at ASC, id ASC").Scan(&rows).Error; err != nil {
		return nil, mapError(err)
	}
	return loadBindings(ctx, repository.db, rows)
}

func (repository *repository) ReplaceBindings(ctx context.Context, assetID uuid.UUID, bindings []asset.MeasurementBinding) error {
	if assetID == uuid.Nil {
		return asset.ErrInvalidAsset
	}
	return mapError(repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked asset.Asset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", assetID).Error; err != nil {
			return err
		}
		if err := tx.Where("binding_id IN (?)", tx.Table("measurement_bindings").Select("id").Where("asset_id = ?", assetID)).Delete(&bindingInputRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("asset_id = ?", assetID).Delete(&bindingRow{}).Error; err != nil {
			return err
		}
		seen := make(map[uuid.UUID]struct{}, len(bindings))
		for index := range bindings {
			binding := &bindings[index]
			binding.OwnerAssetID = assetID
			if binding.ID == uuid.Nil {
				binding.ID = uuid.New()
			}
			if _, exists := seen[binding.ID]; exists || validateBinding(*binding) != nil {
				return asset.ErrInvalidBinding
			}
			seen[binding.ID] = struct{}{}
			reference, err := json.Marshal(binding.Semantic.Reference)
			if err != nil {
				return asset.ErrInvalidBinding
			}
			row := rowFromBinding(*binding, reference)
			if err := tx.Table("measurement_bindings").Create(&row).Error; err != nil {
				return err
			}
		}
		for _, binding := range bindings {
			for _, inputID := range binding.VirtualInputs {
				if err := tx.Table("measurement_binding_inputs").Create(&bindingInputRow{BindingID: binding.ID, InputBindingID: inputID}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}))
}

type bindingInputRow struct {
	BindingID      uuid.UUID `gorm:"column:binding_id"`
	InputBindingID uuid.UUID `gorm:"column:input_binding_id"`
}

func (bindingInputRow) TableName() string { return "measurement_binding_inputs" }
func (bindingRow) TableName() string      { return "measurement_bindings" }

func loadBindings(ctx context.Context, db *gorm.DB, rows []bindingRow) ([]asset.MeasurementBinding, error) {
	result := make([]asset.MeasurementBinding, len(rows))
	byID := make(map[uuid.UUID]int, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for index, row := range rows {
		binding, err := bindingFromRow(row)
		if err != nil {
			return nil, err
		}
		result[index] = binding
		byID[binding.ID] = index
		ids = append(ids, binding.ID)
	}
	if len(ids) == 0 {
		return result, nil
	}
	inputs := make([]bindingInputRow, 0)
	if err := db.WithContext(ctx).Where("binding_id IN ?", ids).Order("binding_id ASC, input_binding_id ASC").Find(&inputs).Error; err != nil {
		return nil, mapError(err)
	}
	for _, input := range inputs {
		if index, exists := byID[input.BindingID]; exists {
			result[index].VirtualInputs = append(result[index].VirtualInputs, input.InputBindingID)
		}
	}
	return result, nil
}

func bindingFromRow(row bindingRow) (asset.MeasurementBinding, error) {
	var reference asset.ReferenceCondition
	if err := json.Unmarshal(row.Reference, &reference); err != nil {
		return asset.MeasurementBinding{}, asset.ErrInvalidBinding
	}
	source := asset.SourceReference{Kind: row.SourceType}
	if row.TagID != nil {
		source.TagID = *row.TagID
	}
	if row.PluginInstanceID != nil {
		source.PluginInstanceID = *row.PluginInstanceID
	}
	if row.OutputKey != nil {
		source.OutputKey = *row.OutputKey
	}
	return asset.MeasurementBinding{ID: row.ID, OwnerAssetID: row.AssetID, BoundaryAssetID: row.BoundaryAssetID, SourceKey: source.Key(), Source: source, Semantic: asset.Semantic{Resource: row.Resource, Quantity: row.Quantity, Unit: row.Unit, Precision: row.Precision, Reference: reference}, MeterRole: row.MeterRole, RollupPolicy: row.RollupPolicy, VirtualInputs: []uuid.UUID{}}, nil
}

func rowFromBinding(binding asset.MeasurementBinding, reference []byte) bindingRow {
	row := bindingRow{ID: binding.ID, AssetID: binding.OwnerAssetID, BoundaryAssetID: binding.BoundaryAssetID, SourceType: binding.Source.Kind, Resource: binding.Semantic.Resource, Quantity: binding.Semantic.Quantity, Unit: binding.Semantic.Unit, Precision: binding.Semantic.Precision, Reference: reference, MeterRole: binding.MeterRole, RollupPolicy: binding.RollupPolicy}
	if binding.Source.Kind == asset.SourceTag {
		row.TagID = &binding.Source.TagID
	} else {
		row.PluginInstanceID = &binding.Source.PluginInstanceID
		row.OutputKey = &binding.Source.OutputKey
	}
	return row
}

func validateBinding(binding asset.MeasurementBinding) error {
	if binding.ID == uuid.Nil || binding.OwnerAssetID == uuid.Nil || binding.BoundaryAssetID == uuid.Nil || binding.Source.Validate() != nil || binding.Semantic.Validate() != nil {
		return asset.ErrInvalidBinding
	}
	switch binding.MeterRole {
	case asset.MeterRoleDirect, asset.MeterRoleMain, asset.MeterRoleSubmeter:
		if len(binding.VirtualInputs) != 0 {
			return asset.ErrInvalidBinding
		}
	case asset.MeterRoleVirtual:
		if len(binding.VirtualInputs) == 0 {
			return asset.ErrInvalidBinding
		}
	default:
		return asset.ErrInvalidBinding
	}
	if binding.RollupPolicy != asset.RollupInclude && binding.RollupPolicy != asset.RollupExclude {
		return asset.ErrInvalidBinding
	}
	return nil
}
func assetLess(left, right asset.Asset) bool {
	if left.Position != right.Position {
		return left.Position < right.Position
	}
	leftName, rightName := strings.ToLower(left.Name), strings.ToLower(right.Name)
	if leftName != rightName {
		return leftName < rightName
	}
	return left.ID.String() < right.ID.String()
}
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return asset.ErrAssetNotFound
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "idx_assets_sibling_name_ci":
		return asset.ErrAssetNameExists
	case "assets_parent_id_fkey", "measurement_bindings_asset_id_fkey", "measurement_bindings_boundary_asset_id_fkey":
		return asset.ErrAssetNotFound
	case "idx_measurement_bindings_tag_owner", "idx_measurement_bindings_plugin_output_owner":
		return asset.ErrBindingSourceExists
	case "measurement_bindings_tag_id_fkey", "measurement_bindings_plugin_instance_id_fkey":
		return asset.ErrBindingSourceMissing
	case "measurement_bindings_source_type_check", "measurement_bindings_source_check", "measurement_bindings_output_key_check", "measurement_bindings_resource_check", "measurement_bindings_quantity_check", "measurement_bindings_unit_check", "measurement_bindings_precision_check", "measurement_bindings_reference_check", "measurement_bindings_meter_role_check", "measurement_bindings_rollup_policy_check", "measurement_binding_inputs_not_self":
		return asset.ErrInvalidBinding
	}
	if pgErr.Code == "23514" {
		if strings.Contains(pgErr.Message, "cycle") {
			return asset.ErrHierarchyCycle
		}
		if strings.Contains(pgErr.Message, "depth") {
			return asset.ErrHierarchyDepth
		}
		if strings.Contains(pgErr.Message, "leaf") || strings.Contains(pgErr.Message, "boundary") || strings.Contains(pgErr.Message, "virtual") {
			return asset.ErrInvalidBinding
		}
		return asset.ErrInvalidAsset
	}
	if pgErr.Code == "23502" || pgErr.Code == "22001" || pgErr.Code == "22P02" {
		return asset.ErrInvalidAsset
	}
	return err
}

func mapDeleteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "assets_parent_id_fkey", "measurement_bindings_asset_id_fkey", "measurement_bindings_boundary_asset_id_fkey":
			return asset.ErrAssetHasDependents
		}
	}
	return mapError(err)
}

var _ asset.Repository = (*repository)(nil)
