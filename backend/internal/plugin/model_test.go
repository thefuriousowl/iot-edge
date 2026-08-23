package plugin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInstanceTableAndJSONContract(t *testing.T) {
	t.Parallel()

	instance := Instance{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Type:          "energy_management",
		Name:          "Plant Energy",
		Enabled:       true,
		Config:        Config(`{"logger_id":"22222222-2222-2222-2222-222222222222"}`),
		ConfigVersion: 1,
		CreatedAt:     time.Date(2026, time.August, 22, 1, 2, 3, 0, time.UTC),
		UpdatedAt:     time.Date(2026, time.August, 22, 4, 5, 6, 0, time.UTC),
	}
	if instance.TableName() != "plugin_instances" {
		t.Fatalf("TableName() = %q", instance.TableName())
	}
	payload, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"id", "type", "name", "enabled", "config", "config_version", "created_at", "updated_at"} {
		if _, exists := decoded[key]; !exists {
			t.Errorf("JSON missing %q: %s", key, payload)
		}
	}
	if len(decoded) != 8 {
		t.Errorf("JSON has unexpected fields: %s", payload)
	}
	config, ok := decoded["config"].(map[string]any)
	if !ok || config["logger_id"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("config = %#v", decoded["config"])
	}
}
