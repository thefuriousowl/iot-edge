package asset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestKindsMatchStableStorageContract(t *testing.T) {
	t.Parallel()

	kinds := []Kind{KindSite, KindBuilding, KindArea, KindSystem, KindEquipment, KindMeter, KindCustom}
	want := []string{"site", "building", "area", "system", "equipment", "meter", "custom"}
	for index, kind := range kinds {
		if !kind.Valid() {
			t.Errorf("kind %q is not valid", kind)
		}
		if string(kind) != want[index] {
			t.Errorf("kind[%d] = %q, want %q", index, kind, want[index])
		}
	}
	for _, kind := range []Kind{"", "device", "utility", "SITE", "future"} {
		if kind.Valid() {
			t.Errorf("kind %q unexpectedly valid", kind)
		}
	}
}

func TestAssetTableAndJSONContract(t *testing.T) {
	t.Parallel()

	parentID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	description := "Primary compressed-air supply"
	timezone := "Asia/Bangkok"
	createdAt := time.Date(2026, time.August, 28, 1, 2, 3, 0, time.UTC)
	entity := Asset{
		ID:          uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ParentID:    &parentID,
		Name:        "Compressor 1",
		Kind:        KindEquipment,
		Description: &description,
		Enabled:     true,
		Timezone:    &timezone,
		Position:    4,
		Metadata:    Metadata(`{"manufacturer":"Example","location":{"building":"A"}}`),
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt.Add(time.Hour),
	}

	if entity.TableName() != "assets" {
		t.Fatalf("TableName() = %q, want assets", entity.TableName())
	}
	payload, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, key := range []string{"id", "parent_id", "name", "kind", "description", "enabled", "timezone", "position", "metadata", "created_at", "updated_at"} {
		if _, exists := decoded[key]; !exists {
			t.Errorf("JSON missing %q: %s", key, payload)
		}
	}
	if len(decoded) != 11 {
		t.Errorf("JSON has unexpected fields: %s", payload)
	}
	if decoded["parent_id"] != parentID.String() || decoded["kind"] != "equipment" || decoded["timezone"] != timezone {
		t.Errorf("identity fields = %#v", decoded)
	}
	metadata, ok := decoded["metadata"].(map[string]any)
	if !ok || metadata["manufacturer"] != "Example" {
		t.Errorf("metadata = %#v", decoded["metadata"])
	}
}

func TestAssetJSONPreservesNullableHierarchyFields(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(Asset{Kind: KindSite, Metadata: Metadata(`{}`)})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, fragment := range []string{`"parent_id":null`, `"description":null`, `"timezone":null`, `"metadata":{}`} {
		if !strings.Contains(string(payload), fragment) {
			t.Errorf("JSON %s does not contain %s", payload, fragment)
		}
	}
}

func TestTreeNodeJSONOwnsRuntimeProjectionAndChildren(t *testing.T) {
	t.Parallel()

	node := TreeNode{
		Asset:            Asset{ID: uuid.New(), Name: "Factory", Kind: KindSite, Enabled: true, Metadata: Metadata(`{}`)},
		Depth:            0,
		EffectiveEnabled: true,
		MeasurementCount: 3,
		Children: []TreeNode{{
			Asset:            Asset{ID: uuid.New(), Name: "Utilities", Kind: KindArea, Enabled: false, Metadata: Metadata(`{}`)},
			Depth:            1,
			EffectiveEnabled: false,
			Children:         []TreeNode{},
		}},
	}

	payload, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	children, ok := decoded["children"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("children = %#v", decoded["children"])
	}
	if decoded["depth"] != float64(0) || decoded["effective_enabled"] != true || decoded["measurement_count"] != float64(3) {
		t.Errorf("runtime projection = %#v", decoded)
	}
	child, ok := children[0].(map[string]any)
	if !ok || child["kind"] != "area" || child["effective_enabled"] != false {
		t.Errorf("child = %#v", children[0])
	}
}
