package tag

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTagDomainEnumsMatchStorageContract(t *testing.T) {
	t.Parallel()
	tagTypes := []Type{TypeReading, TypeConstant, TypeCalculated}
	wantTagTypes := []string{"reading", "constant", "calculated"}
	for index, tagType := range tagTypes {
		if string(tagType) != wantTagTypes[index] {
			t.Errorf("tag type[%d] = %q, want %q", index, tagType, wantTagTypes[index])
		}
	}

	dataTypes := []DataType{DataTypeBool, DataTypeInt16, DataTypeUInt16, DataTypeInt32, DataTypeUInt32, DataTypeFloat32, DataTypeFloat64}
	wantDataTypes := []string{"bool", "int16", "uint16", "int32", "uint32", "float32", "float64"}
	for index, dataType := range dataTypes {
		if string(dataType) != wantDataTypes[index] {
			t.Errorf("data type[%d] = %q, want %q", index, dataType, wantDataTypes[index])
		}
	}
}

func TestTagTableNamesMatchMigration(t *testing.T) {
	t.Parallel()
	if got := (Tag{}).TableName(); got != "tags" {
		t.Errorf("Tag table name = %q, want tags", got)
	}
	if got := (Dependency{}).TableName(); got != "tag_dependencies" {
		t.Errorf("Dependency table name = %q, want tag_dependencies", got)
	}
}

func TestTagJSONPreservesProtocolNeutralConfigAndNullableDatasource(t *testing.T) {
	t.Parallel()
	datasourceID := uuid.MustParse("9c7d5331-7df1-46d8-b2e4-6eb8bd8ab6ca")
	createdAt := time.Date(2026, time.August, 21, 15, 0, 0, 0, time.UTC)
	entity := Tag{
		ID:           uuid.MustParse("7f90aa37-fc38-49fb-807b-9f8c788e7de9"),
		DatasourceID: &datasourceID,
		Name:         "Line voltage",
		Type:         TypeReading,
		DataType:     DataTypeFloat32,
		Enabled:      true,
		Config:       json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0,"byte_order":"big_endian"}}}`),
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}

	encoded, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body["datasource_id"] != datasourceID.String() || body["type"] != "reading" || body["data_type"] != "float32" {
		t.Errorf("identity fields = %#v", body)
	}
	config, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want JSON object", body["config"])
	}
	decoder, ok := config["decoder"].(map[string]any)
	if !ok || decoder["type"] != "binary_numeric" {
		t.Errorf("decoder = %#v", config["decoder"])
	}

	encoded, err = json.Marshal(Tag{DatasourceID: nil, Description: nil})
	if err != nil {
		t.Fatalf("json.Marshal() nullable tag error = %v", err)
	}
	if !strings.Contains(string(encoded), `"datasource_id":null`) || !strings.Contains(string(encoded), `"description":null`) {
		t.Errorf("nullable JSON = %s", encoded)
	}
}

func TestDependencyJSONUsesStableTagIDs(t *testing.T) {
	t.Parallel()
	dependency := Dependency{
		TagID:          uuid.MustParse("71623fd7-e996-4617-9136-7386fd43ec4d"),
		DependsOnTagID: uuid.MustParse("c0598e92-c1d6-4108-9557-627367b2477f"),
	}
	encoded, err := json.Marshal(dependency)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(encoded) != `{"tag_id":"71623fd7-e996-4617-9136-7386fd43ec4d","depends_on_tag_id":"c0598e92-c1d6-4108-9557-627367b2477f"}` {
		t.Errorf("dependency JSON = %s", encoded)
	}
}
