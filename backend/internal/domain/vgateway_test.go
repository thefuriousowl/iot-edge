package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVGatewayTableNames_MatchDocumentedSchema(t *testing.T) {
	if got := (VGateway{}).TableName(); got != "vgateways" {
		t.Errorf("VGateway table name = %q, want vgateways", got)
	}
	if got := (VGatewayStats{}).TableName(); got != "vgateway_stats" {
		t.Errorf("VGatewayStats table name = %q, want vgateway_stats", got)
	}
}

func TestVGatewayJSON_MatchesAPIContract(t *testing.T) {
	description := "Connection to main factory PLC"
	createdAt := time.Date(2026, time.August, 21, 8, 0, 0, 0, time.UTC)
	gateway := VGateway{
		ID:          uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Name:        "Main PLC Gateway",
		Type:        VGatewayTypeModbusTCP,
		Description: &description,
		Enabled:     true,
		Config: ModbusTCPConfig{
			Host:              "192.168.1.100",
			Port:              502,
			Timeout:           5_000,
			RetryCount:        3,
			RetryDelay:        1_000,
			KeepAlive:         true,
			ReconnectInterval: 30,
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}

	encoded, err := json.Marshal(gateway)
	if err != nil {
		t.Fatalf("marshalling VGateway: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("unmarshalling VGateway JSON: %v", err)
	}
	if body["type"] != "modbus_tcp" {
		t.Errorf("type = %v, want modbus_tcp", body["type"])
	}
	config, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want a JSON object", body["config"])
	}
	if config["retry_count"] != float64(3) {
		t.Errorf("retry_count = %v, want 3", config["retry_count"])
	}
	if config["reconnect_interval"] != float64(30) {
		t.Errorf("reconnect_interval = %v, want 30", config["reconnect_interval"])
	}
}

func TestVGatewayJSON_PreservesNullableDescription(t *testing.T) {
	encoded, err := json.Marshal(VGateway{Description: nil})
	if err != nil {
		t.Fatalf("marshalling VGateway: %v", err)
	}
	if !strings.Contains(string(encoded), `"description":null`) {
		t.Errorf("JSON = %s, want nullable description", encoded)
	}
}

func TestVGatewayConnectionStatuses_MatchLifecycleContract(t *testing.T) {
	statuses := []VGatewayConnectionStatus{
		VGatewayStatusStopped,
		VGatewayStatusConnecting,
		VGatewayStatusConnected,
		VGatewayStatusDisconnected,
		VGatewayStatusError,
	}
	want := []string{"stopped", "connecting", "connected", "disconnected", "error"}

	for index, status := range statuses {
		if string(status) != want[index] {
			t.Errorf("status[%d] = %q, want %q", index, status, want[index])
		}
	}
}

func TestVGatewayStatsJSON_OmitsLoadedGatewayRelation(t *testing.T) {
	stats := VGatewayStats{
		VGatewayID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		VGateway: &VGateway{
			Name: "must not be serialized",
		},
	}

	encoded, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("marshalling VGatewayStats: %v", err)
	}
	if strings.Contains(string(encoded), "must not be serialized") {
		t.Errorf("VGatewayStats JSON exposes the loaded relation: %s", encoded)
	}
}
