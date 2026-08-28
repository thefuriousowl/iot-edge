package assetpostgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/migrations"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryHierarchyCRUDProjectionsAndContext_Integration(t *testing.T) {
	database := newAssetRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()

	site := asset.Asset{Name: "Plant", Kind: asset.KindSite, Enabled: true, Position: 0}
	if err := repository.Create(ctx, &site); err != nil {
		t.Fatal(err)
	}
	building := asset.Asset{ParentID: &site.ID, Name: "Building B", Kind: asset.KindBuilding, Enabled: true, Position: 2}
	area := asset.Asset{ParentID: &site.ID, Name: "Area A", Kind: asset.KindArea, Enabled: false, Position: 1}
	meter := asset.Asset{ParentID: &area.ID, Name: "Main meter", Kind: asset.KindMeter, Enabled: true, Position: 0}
	for _, entity := range []*asset.Asset{&building, &area} {
		if err := repository.Create(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	meter.ParentID = &area.ID
	if err := repository.Create(ctx, &meter); err != nil {
		t.Fatal(err)
	}

	children, err := repository.ListChildren(ctx, &site.ID)
	if err != nil || len(children) != 2 || children[0].ID != area.ID || children[1].ID != building.ID {
		t.Fatalf("children = %#v, %v", children, err)
	}
	roots, err := repository.ListChildren(ctx, nil)
	if err != nil || len(roots) != 1 || roots[0].ID != site.ID {
		t.Fatalf("roots = %#v, %v", roots, err)
	}
	result, err := repository.List(ctx, asset.ListInput{Search: "%", Page: 1, PerPage: 10})
	if err != nil || result.Total != 0 {
		t.Fatalf("escaped search = %#v, %v", result, err)
	}
	result, err = repository.List(ctx, asset.ListInput{Search: "a", Page: 2, PerPage: 2})
	if err != nil || result.Total != 3 || result.TotalPages != 2 || len(result.Data) != 1 {
		t.Fatalf("paged list = %#v, %v", result, err)
	}
	if _, err := repository.List(ctx, asset.ListInput{PerPage: asset.MaxAssetsPerPage + 1}); !errors.Is(err, asset.ErrInvalidAssetList) {
		t.Fatalf("oversized list error = %v", err)
	}

	ancestors, err := repository.Ancestors(ctx, meter.ID)
	if err != nil || len(ancestors) != 2 || ancestors[0].ID != site.ID || ancestors[1].ID != area.ID {
		t.Fatalf("ancestors = %#v, %v", ancestors, err)
	}
	tree, err := repository.Subtree(ctx, site.ID)
	if err != nil || tree.ID != site.ID || !tree.EffectiveEnabled || len(tree.Children) != 2 || tree.Children[0].ID != area.ID || tree.Children[0].EffectiveEnabled || len(tree.Children[0].Children) != 1 || tree.Children[0].Children[0].EffectiveEnabled {
		t.Fatalf("tree = %#v, %v", tree, err)
	}

	building.Name = "Administration"
	building.Description = stringPointer("Updated")
	building.Metadata = asset.Metadata(`{"floor":2}`)
	if err := repository.Update(ctx, &building); err != nil {
		t.Fatal(err)
	}
	found, err := repository.Find(ctx, building.ID)
	if err != nil || found.Name != "Administration" || found.Description == nil || *found.Description != "Updated" {
		t.Fatalf("updated = %#v, %v", found, err)
	}
	if err := repository.Move(ctx, building.ID, &area.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := repository.Move(ctx, area.ID, &meter.ID, 0); !errors.Is(err, asset.ErrHierarchyCycle) {
		t.Fatalf("cycle move error = %v", err)
	}
	if err := repository.Delete(ctx, area.ID); !errors.Is(err, asset.ErrAssetHasDependents) {
		t.Fatalf("dependent delete error = %v", err)
	}
	if err := repository.Delete(ctx, uuid.New()); !errors.Is(err, asset.ErrAssetNotFound) {
		t.Fatalf("missing delete error = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repository.ListChildren(cancelled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}

func TestRepositoryAtomicallyReplacesAndProjectsBindings_Integration(t *testing.T) {
	database := newAssetRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	site := asset.Asset{Name: "Binding plant", Kind: asset.KindSite, Enabled: true}
	if err := repository.Create(ctx, &site); err != nil {
		t.Fatal(err)
	}
	meter := asset.Asset{ParentID: &site.ID, Name: "Binding meter", Kind: asset.KindMeter, Enabled: true}
	if err := repository.Create(ctx, &meter); err != nil {
		t.Fatal(err)
	}
	tagIDs := insertConstantTags(t, database, "Binding", 5)
	pluginID := insertPlugin(t, database)
	inputID, virtualID := uuid.New(), uuid.New()
	bindings := []asset.MeasurementBinding{
		binding(inputID, meter.ID, site.ID, asset.TagSource(tagIDs[0]), asset.MeterRoleSubmeter, nil),
		binding(virtualID, meter.ID, site.ID, asset.PluginOutputSource(pluginID, "electrical_demand_kw"), asset.MeterRoleVirtual, []uuid.UUID{inputID}),
	}
	if err := repository.ReplaceBindings(ctx, meter.ID, bindings); err != nil {
		t.Fatal(err)
	}
	listed, err := repository.ListBindings(ctx, meter.ID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("bindings = %#v, %v", listed, err)
	}
	byID := map[uuid.UUID]asset.MeasurementBinding{}
	for _, item := range listed {
		byID[item.ID] = item
	}
	if byID[inputID].Source.TagID != tagIDs[0] || len(byID[virtualID].VirtualInputs) != 1 || byID[virtualID].VirtualInputs[0] != inputID {
		t.Fatalf("binding projection = %#v", listed)
	}
	reverse, err := repository.ListBindingsByTag(ctx, tagIDs[0])
	if err != nil || len(reverse) != 1 || reverse[0].ID != inputID || reverse[0].OwnerAssetID != meter.ID {
		t.Fatalf("reverse Tag bindings = %#v, %v", reverse, err)
	}
	emptyReverse, err := repository.ListBindingsByTag(ctx, uuid.New())
	if err != nil || emptyReverse == nil || len(emptyReverse) != 0 {
		t.Fatalf("empty reverse Tag bindings = %#v, %v", emptyReverse, err)
	}
	tree, _ := repository.Subtree(ctx, site.ID)
	if tree.Children[0].MeasurementCount != 2 {
		t.Fatalf("measurement count = %d", tree.Children[0].MeasurementCount)
	}

	invalid := []asset.MeasurementBinding{binding(uuid.New(), meter.ID, site.ID, asset.TagSource(tagIDs[1]), asset.MeterRoleDirect, nil), binding(uuid.New(), meter.ID, site.ID, asset.TagSource(uuid.New()), asset.MeterRoleDirect, nil)}
	if err := repository.ReplaceBindings(ctx, meter.ID, invalid); !errors.Is(err, asset.ErrBindingSourceMissing) {
		t.Fatalf("invalid replacement error = %v", err)
	}
	afterRollback, _ := repository.ListBindings(ctx, meter.ID)
	rollbackIDs := map[uuid.UUID]bool{}
	for _, item := range afterRollback {
		rollbackIDs[item.ID] = true
	}
	if len(afterRollback) != 2 || !rollbackIDs[inputID] || !rollbackIDs[virtualID] {
		t.Fatalf("replacement was not atomic: %#v", afterRollback)
	}

	sets := [][]asset.MeasurementBinding{
		{binding(uuid.New(), meter.ID, site.ID, asset.TagSource(tagIDs[1]), asset.MeterRoleDirect, nil), binding(uuid.New(), meter.ID, site.ID, asset.TagSource(tagIDs[2]), asset.MeterRoleDirect, nil)},
		{binding(uuid.New(), meter.ID, site.ID, asset.TagSource(tagIDs[3]), asset.MeterRoleDirect, nil), binding(uuid.New(), meter.ID, site.ID, asset.TagSource(tagIDs[4]), asset.MeterRoleDirect, nil)},
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for _, set := range sets {
		set := set
		wait.Add(1)
		go func() { defer wait.Done(); <-start; errs <- repository.ReplaceBindings(ctx, meter.ID, set) }()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent replacement error = %v", err)
		}
	}
	final, _ := repository.ListBindings(ctx, meter.ID)
	if len(final) != 2 {
		t.Fatalf("mixed replacement = %#v", final)
	}
	keys := []string{final[0].Source.Key(), final[1].Source.Key()}
	sort.Strings(keys)
	first := []string{asset.TagSource(tagIDs[1]).Key(), asset.TagSource(tagIDs[2]).Key()}
	second := []string{asset.TagSource(tagIDs[3]).Key(), asset.TagSource(tagIDs[4]).Key()}
	sort.Strings(first)
	sort.Strings(second)
	if strings.Join(keys, ",") != strings.Join(first, ",") && strings.Join(keys, ",") != strings.Join(second, ",") {
		t.Fatalf("mixed source set = %v", keys)
	}
}

func newAssetRepositoryDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Asset repository integration tests")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "asset_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	database, err := gorm.Open(gormpostgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrations.Up(context.Background(), sqlDB); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	return database
}

func insertConstantTags(t *testing.T, database *gorm.DB, prefix string, count int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, count)
	for index := 0; index < count; index++ {
		var rawID string
		if err := database.Raw(`INSERT INTO tags(name,type,data_type,config) VALUES (?, 'constant','float64','{"value":1}') RETURNING id`, prefix+" tag "+string(rune('A'+index))).Scan(&rawID).Error; err != nil {
			t.Fatal(err)
		}
		id, err := uuid.Parse(rawID)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}
func insertPlugin(t *testing.T, database *gorm.DB) uuid.UUID {
	t.Helper()
	var rawID string
	if err := database.Raw(`INSERT INTO plugin_instances(type,name,config_version) VALUES ('energy_management','Binding Energy',1) RETURNING id`).Scan(&rawID).Error; err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func binding(id, owner, boundary uuid.UUID, source asset.SourceReference, role asset.MeterRole, inputs []uuid.UUID) asset.MeasurementBinding {
	return asset.MeasurementBinding{ID: id, OwnerAssetID: owner, BoundaryAssetID: boundary, Source: source, Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 3}, MeterRole: role, RollupPolicy: asset.RollupInclude, VirtualInputs: inputs}
}
func stringPointer(value string) *string { return &value }
