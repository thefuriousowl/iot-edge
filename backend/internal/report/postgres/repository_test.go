package reportpostgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/report"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryCRUDOrderedColumnsAtomicReplacementAndCascades_Integration(t *testing.T) {
	database := newReportRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	tags := insertReportTags(t, database, "Power", "Running", "Temperature")
	loggerA := insertReportLogger(t, database, "Primary Logger", tags[0], tags[1])
	loggerB := insertReportLogger(t, database, "Secondary Logger", tags[0], tags[2])
	description := "Operations export"
	entity := &report.Report{
		Name:        "Shift Summary",
		Description: &description,
		LoggerID:    loggerA,
		Timezone:    "Asia/Bangkok",
		Mode:        datalogger.QueryModeRaw,
		Columns: []report.Column{
			{LoggerID: loggerA, TagID: tags[1], Position: 1, Name: "Running"},
			{LoggerID: loggerA, TagID: tags[0], Position: 0, Name: "Demand"},
		},
	}
	if err := repository.Create(ctx, entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if entity.ID == uuid.Nil {
		t.Fatal("Create() did not assign a Report ID")
	}

	found, err := repository.Find(ctx, entity.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found.Name != "Shift Summary" || found.Description == nil || *found.Description != description || found.LoggerID != loggerA || found.LoggerName != "Primary Logger" || found.Timezone != "Asia/Bangkok" || found.Mode != datalogger.QueryModeRaw || found.Bucket != "" || found.ColumnCount != 2 || found.CreatedAt.IsZero() || found.UpdatedAt.IsZero() {
		t.Fatalf("Find() = %#v", found)
	}
	if len(found.Columns) != 2 || found.Columns[0].TagID != tags[0] || found.Columns[0].Position != 0 || found.Columns[0].Name != "Demand" || found.Columns[0].Aggregate != "" || found.Columns[0].TagName != "Power" || found.Columns[0].TagType != "constant" || found.Columns[0].DataType != "float64" || found.Columns[1].TagID != tags[1] || found.Columns[1].Position != 1 || found.Columns[1].DataType != "bool" {
		t.Fatalf("Find().Columns = %#v", found.Columns)
	}

	listed, err := repository.List(ctx, report.ListInput{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listed.Total != 1 || listed.TotalPages != 1 || len(listed.Data) != 1 || listed.Data[0].ID != entity.ID || listed.Data[0].LoggerName != "Primary Logger" || listed.Data[0].ColumnCount != 2 || listed.Data[0].Columns != nil {
		t.Fatalf("List() = %#v", listed)
	}

	originalCreatedAt := found.CreatedAt
	failed := cloneReport(found)
	failed.Name = "Must Roll Back"
	failed.LoggerID = loggerB
	failed.Mode = datalogger.QueryModeAggregate
	failed.Bucket = datalogger.QueryBucket5Minutes
	failed.Columns = []report.Column{{LoggerID: loggerB, TagID: tags[1], Position: 0, Name: "Not selected", Aggregate: datalogger.AggregateCount}}
	if err := repository.Update(ctx, failed); !errors.Is(err, report.ErrTagNotSelected) {
		t.Fatalf("Update(non-selected Tag) error = %v, want %v", err, report.ErrTagNotSelected)
	}
	rolledBack, err := repository.Find(ctx, entity.ID)
	if err != nil || rolledBack.Name != "Shift Summary" || rolledBack.LoggerID != loggerA || rolledBack.Mode != datalogger.QueryModeRaw || rolledBack.Bucket != "" || len(rolledBack.Columns) != 2 || rolledBack.Columns[0].TagID != tags[0] || rolledBack.Columns[1].TagID != tags[1] {
		t.Fatalf("Report after failed replacement = %#v, %v", rolledBack, err)
	}

	updated := cloneReport(rolledBack)
	updated.Name = "Energy Summary"
	updated.Description = nil
	updated.LoggerID = loggerB
	updated.Timezone = "UTC"
	updated.Mode = datalogger.QueryModeAggregate
	updated.Bucket = datalogger.QueryBucket15Minutes
	updated.Columns = []report.Column{
		{LoggerID: loggerB, TagID: tags[2], Position: 1, Name: "Average Temperature", Aggregate: datalogger.AggregateAvg},
		{LoggerID: loggerB, TagID: tags[0], Position: 0, Name: "Maximum Demand", Aggregate: datalogger.AggregateMax},
	}
	if err := repository.Update(ctx, updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	replaced, err := repository.Find(ctx, entity.ID)
	if err != nil {
		t.Fatalf("Find(updated) error = %v", err)
	}
	if replaced.Name != "Energy Summary" || replaced.Description != nil || replaced.LoggerID != loggerB || replaced.LoggerName != "Secondary Logger" || replaced.Timezone != "UTC" || replaced.Mode != datalogger.QueryModeAggregate || replaced.Bucket != datalogger.QueryBucket15Minutes || replaced.ColumnCount != 2 || replaced.Columns[0].TagID != tags[0] || replaced.Columns[0].Aggregate != datalogger.AggregateMax || replaced.Columns[1].TagID != tags[2] || replaced.Columns[1].Aggregate != datalogger.AggregateAvg || !replaced.CreatedAt.Equal(originalCreatedAt) {
		t.Fatalf("replaced Report = %#v", replaced)
	}

	if err := repository.Delete(ctx, entity.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertReportRowCount(t, database, "report_columns", "report_id", entity.ID, 0)
	if _, err := repository.Find(ctx, entity.ID); !errors.Is(err, report.ErrNotFound) {
		t.Fatalf("Find(deleted) error = %v", err)
	}

	cascade := &report.Report{Name: "Logger Cascade", LoggerID: loggerA, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{{LoggerID: loggerA, TagID: tags[0], Position: 0, Name: "Power"}}}
	if err := repository.Create(ctx, cascade); err != nil {
		t.Fatalf("Create(cascade) error = %v", err)
	}
	if err := database.Exec("DELETE FROM data_loggers WHERE id=?", loggerA).Error; err != nil {
		t.Fatalf("deleting source Logger: %v", err)
	}
	assertReportRowCount(t, database, "reports", "id", cascade.ID, 0)
	assertReportRowCount(t, database, "report_columns", "report_id", cascade.ID, 0)
}

func TestRepositoryListFiltersLiteralSearchAndPagination_Integration(t *testing.T) {
	database := newReportRepositoryDatabase(t)
	repository := NewRepository(database)
	tags := insertReportTags(t, database, "Power")
	loggerPercent := insertReportLogger(t, database, "Plant % Logger", tags[0])
	loggerUnderscore := insertReportLogger(t, database, "Plant_Logger", tags[0])
	loggerBackslash := insertReportLogger(t, database, `Plant\Logger`, tags[0])

	alpha := insertReport(t, repository, "Alpha", loggerPercent, datalogger.QueryModeRaw, "", tags[0])
	beta := insertReport(t, repository, "beta", loggerPercent, datalogger.QueryModeAggregate, datalogger.QueryBucket1Hour, tags[0])
	percent := insertReport(t, repository, "Report % Exact", loggerUnderscore, datalogger.QueryModeRaw, "", tags[0])
	underscore := insertReport(t, repository, "Report_Exact", loggerBackslash, datalogger.QueryModeAggregate, datalogger.QueryBucket1Day, tags[0])
	backslash := insertReport(t, repository, `Report\Exact`, loggerUnderscore, datalogger.QueryModeRaw, "", tags[0])

	base := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	for index, entity := range []*report.Report{alpha, beta, percent, underscore, backslash} {
		if err := database.Exec("UPDATE reports SET created_at=?,updated_at=? WHERE id=?", base.Add(time.Duration(index)*time.Minute), base.Add(time.Duration(index)*time.Minute), entity.ID).Error; err != nil {
			t.Fatalf("setting Report order: %v", err)
		}
	}

	assertReportList(t, repository, report.ListInput{Search: "%", Page: 1, PerPage: 20}, []uuid.UUID{alpha.ID, beta.ID, percent.ID})
	assertReportList(t, repository, report.ListInput{Search: "_", Page: 1, PerPage: 20}, []uuid.UUID{percent.ID, underscore.ID, backslash.ID})
	assertReportList(t, repository, report.ListInput{Search: `\`, Page: 1, PerPage: 20}, []uuid.UUID{underscore.ID, backslash.ID})
	assertReportList(t, repository, report.ListInput{Search: `' UNION SELECT NULL --`, Page: 1, PerPage: 20}, []uuid.UUID{})
	assertReportList(t, repository, report.ListInput{LoggerID: &loggerPercent, Page: 1, PerPage: 20}, []uuid.UUID{alpha.ID, beta.ID})
	aggregate := datalogger.QueryModeAggregate
	assertReportList(t, repository, report.ListInput{Mode: &aggregate, Page: 1, PerPage: 20}, []uuid.UUID{beta.ID, underscore.ID})
	assertReportList(t, repository, report.ListInput{Page: 2, PerPage: 2}, []uuid.UUID{percent.ID, underscore.ID})

	filtered, err := repository.List(context.Background(), report.ListInput{LoggerID: &loggerPercent, Mode: &aggregate, Search: "Plant_Logger", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("List(combined filters) error = %v", err)
	}
	if filtered.Total != 0 || filtered.TotalPages != 0 || len(filtered.Data) != 0 || filtered.Data == nil {
		t.Fatalf("List(combined filters) leaked OR branch = %#v", filtered)
	}

	page, err := repository.List(context.Background(), report.ListInput{Page: 3, PerPage: 2})
	if err != nil {
		t.Fatalf("List(last page) error = %v", err)
	}
	if page.Total != 5 || page.TotalPages != 3 || len(page.Data) != 1 || page.Data[0].ID != backslash.ID || page.Data[0].ColumnCount != 1 || page.Data[0].Columns != nil {
		t.Fatalf("List(last page) = %#v", page)
	}
}

func TestRepositoryMapsConstraintsMissingRowsAndCancellation_Integration(t *testing.T) {
	database := newReportRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	tags := insertReportTags(t, database, "Power", "Running", "Temperature")
	loggerID := insertReportLogger(t, database, "Validation Logger", tags[0], tags[1])
	valid := insertReport(t, repository, "Valid", loggerID, datalogger.QueryModeRaw, "", tags[0])

	duplicateName := &report.Report{Name: "Valid", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{{LoggerID: loggerID, TagID: tags[0], Position: 0, Name: "Power"}}}
	if err := repository.Create(ctx, duplicateName); !errors.Is(err, report.ErrNameExists) {
		t.Errorf("Create(duplicate name) error = %v", err)
	}
	assertReportRowCount(t, database, "reports", "id", duplicateName.ID, 0)

	duplicateAlias := &report.Report{Name: "Duplicate Alias", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{
		{LoggerID: loggerID, TagID: tags[0], Position: 0, Name: "Value"},
		{LoggerID: loggerID, TagID: tags[1], Position: 1, Name: " value "},
	}}
	if err := repository.Create(ctx, duplicateAlias); !errors.Is(err, report.ErrColumnNameExists) {
		t.Errorf("Create(duplicate alias) error = %v, want %v", err, report.ErrColumnNameExists)
	}
	assertReportRowCount(t, database, "reports", "id", duplicateAlias.ID, 0)

	nonSelected := &report.Report{Name: "Non Selected", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{{LoggerID: loggerID, TagID: tags[2], Position: 0, Name: "Temperature"}}}
	if err := repository.Create(ctx, nonSelected); !errors.Is(err, report.ErrTagNotSelected) {
		t.Errorf("Create(non-selected Tag) error = %v", err)
	}
	assertReportRowCount(t, database, "reports", "id", nonSelected.ID, 0)

	missingLogger := &report.Report{Name: "Missing Logger", LoggerID: uuid.New(), Timezone: "UTC", Mode: datalogger.QueryModeRaw}
	if err := repository.Create(ctx, missingLogger); !errors.Is(err, report.ErrInvalidInput) {
		t.Errorf("Create(missing Logger) error = %v", err)
	}
	rawBucket := &report.Report{Name: "Raw Bucket Normalized", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Bucket: datalogger.QueryBucket1Hour, Columns: []report.Column{{LoggerID: loggerID, TagID: tags[0], Position: 0, Name: "Power"}}}
	if err := repository.Create(ctx, rawBucket); err != nil {
		t.Errorf("Create(raw bucket) error = %v", err)
	} else if normalized, findErr := repository.Find(ctx, rawBucket.ID); findErr != nil || normalized.Bucket != "" {
		t.Errorf("Find(raw bucket) = %#v, %v", normalized, findErr)
	}
	invalidPosition := &report.Report{Name: "Invalid Position", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{{LoggerID: loggerID, TagID: tags[0], Position: -1, Name: "Power"}}}
	if err := repository.Create(ctx, invalidPosition); !errors.Is(err, report.ErrInvalidReport) {
		t.Errorf("Create(invalid position) error = %v", err)
	}
	duplicatePosition := &report.Report{Name: "Duplicate Position", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{
		{LoggerID: loggerID, TagID: tags[0], Position: 0, Name: "Power"},
		{LoggerID: loggerID, TagID: tags[1], Position: 0, Name: "Running"},
	}}
	if err := repository.Create(ctx, duplicatePosition); !errors.Is(err, report.ErrInvalidReport) {
		t.Errorf("Create(duplicate position) error = %v", err)
	}
	invalidAggregate := &report.Report{Name: "Invalid Aggregate", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket1Hour, Columns: []report.Column{{LoggerID: loggerID, TagID: tags[0], Position: 0, Name: "Power", Aggregate: "median"}}}
	if err := repository.Create(ctx, invalidAggregate); !errors.Is(err, report.ErrInvalidReport) {
		t.Errorf("Create(invalid aggregate) error = %v", err)
	}
	missingBucket := &report.Report{Name: "Missing Bucket", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeAggregate}
	if err := repository.Create(ctx, missingBucket); !errors.Is(err, report.ErrInvalidReport) {
		t.Errorf("Create(missing aggregate bucket) error = %v", err)
	}

	missingID := uuid.New()
	if _, err := repository.Find(ctx, missingID); !errors.Is(err, report.ErrNotFound) {
		t.Errorf("Find(missing) error = %v", err)
	}
	if err := repository.Update(ctx, &report.Report{ID: missingID, Name: "Missing", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw}); !errors.Is(err, report.ErrNotFound) {
		t.Errorf("Update(missing) error = %v", err)
	}
	if err := repository.Delete(ctx, missingID); !errors.Is(err, report.ErrNotFound) {
		t.Errorf("Delete(missing) error = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	cancelledCreate := &report.Report{Name: "Cancelled", LoggerID: loggerID, Timezone: "UTC", Mode: datalogger.QueryModeRaw, Columns: []report.Column{{LoggerID: loggerID, TagID: tags[0], Position: 0, Name: "Power"}}}
	operations := map[string]func() error{
		"create": func() error { return repository.Create(cancelled, cancelledCreate) },
		"find": func() error {
			_, err := repository.Find(cancelled, valid.ID)
			return err
		},
		"list": func() error {
			_, err := repository.List(cancelled, report.ListInput{Page: 1, PerPage: 20})
			return err
		},
		"update": func() error {
			copy := cloneReport(valid)
			copy.Name = "Cancelled Update"
			return repository.Update(cancelled, copy)
		},
		"delete": func() error { return repository.Delete(cancelled, valid.ID) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
		})
	}
	reloaded, err := repository.Find(ctx, valid.ID)
	if err != nil || reloaded.Name != "Valid" || len(reloaded.Columns) != 1 {
		t.Fatalf("Report changed after cancelled operations: %#v, %v", reloaded, err)
	}
}

func assertReportList(t *testing.T, repository report.Repository, input report.ListInput, want []uuid.UUID) {
	t.Helper()
	result, err := repository.List(context.Background(), input)
	if err != nil {
		t.Fatalf("List(%#v) error = %v", input, err)
	}
	if len(result.Data) != len(want) {
		t.Fatalf("List(%#v) returned %d Reports, want %d: %#v", input, len(result.Data), len(want), result.Data)
	}
	for index, id := range want {
		if result.Data[index].ID != id {
			t.Errorf("List(%#v)[%d] ID = %s, want %s", input, index, result.Data[index].ID, id)
		}
	}
}

func insertReport(t *testing.T, repository report.Repository, name string, loggerID uuid.UUID, mode datalogger.QueryMode, bucket datalogger.QueryBucket, tagID uuid.UUID) *report.Report {
	t.Helper()
	aggregate := datalogger.AggregateFunction("")
	if mode == datalogger.QueryModeAggregate {
		aggregate = datalogger.AggregateAvg
	}
	entity := &report.Report{Name: name, LoggerID: loggerID, Timezone: "UTC", Mode: mode, Bucket: bucket, Columns: []report.Column{{LoggerID: loggerID, TagID: tagID, Position: 0, Name: "Value", Aggregate: aggregate}}}
	if err := repository.Create(context.Background(), entity); err != nil {
		t.Fatalf("creating Report %q: %v", name, err)
	}
	return entity
}

func insertReportTags(t *testing.T, database *gorm.DB, names ...string) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(names))
	for _, name := range names {
		var id uuid.UUID
		dataType := "float64"
		config := `{"value":0}`
		if name == "Running" {
			dataType = "bool"
			config = `{"value":true}`
		}
		if err := database.Raw(`
			INSERT INTO tags (name,type,data_type,enabled,config)
			VALUES (?,'constant',?,true,CAST(? AS jsonb)) RETURNING id
		`, name, dataType, config).Row().Scan(&id); err != nil {
			t.Fatalf("inserting Tag %q: %v", name, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func insertReportLogger(t *testing.T, database *gorm.DB, name string, tagIDs ...uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := database.Raw(`
		INSERT INTO data_loggers (name,enabled,timezone,mode,start_at,config)
		VALUES (?,true,'UTC','interval',TIMESTAMPTZ '2026-08-24 00:00:00Z',CAST(? AS jsonb)) RETURNING id
	`, name, `{"interval_seconds":60}`).Row().Scan(&id); err != nil {
		t.Fatalf("inserting Data Logger %q: %v", name, err)
	}
	for position, tagID := range tagIDs {
		if err := database.Exec("INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES (?,?,?)", id, tagID, position).Error; err != nil {
			t.Fatalf("selecting Tag %s for Logger %q: %v", tagID, name, err)
		}
	}
	return id
}

func assertReportRowCount(t *testing.T, database *gorm.DB, tableName, columnName string, id uuid.UUID, want int64) {
	t.Helper()
	var count int64
	if err := database.Table(tableName).Where(columnName+" = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("counting %s: %v", tableName, err)
	}
	if count != want {
		t.Errorf("%s count = %d, want %d", tableName, count, want)
	}
}

func cloneReport(entity *report.Report) *report.Report {
	copy := *entity
	copy.Columns = append([]report.Column(nil), entity.Columns...)
	return &copy
}

func newReportRepositoryDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Report repository integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "report_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDatabase.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDatabase.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
			t.Errorf("dropping schema: %v", err)
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	database, err := gorm.Open(gormpostgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening GORM: %v", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatalf("getting SQL DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	for _, migration := range []string{
		"../../../migrations/000002_create_vgateways.up.sql",
		"../../../migrations/000003_create_devices_datasources.up.sql",
		"../../../migrations/000004_create_tags.up.sql",
		"../../../migrations/000006_create_data_loggers.up.sql",
		"../../../migrations/000009_create_reports.up.sql",
		"../../../migrations/000018_enforce_report_aggregate_bucket.up.sql",
	} {
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatalf("reading migration %s: %v", migration, err)
		}
		if err := database.Exec(string(contents)).Error; err != nil {
			t.Fatalf("applying migration %s: %v", migration, err)
		}
	}
	return database
}
