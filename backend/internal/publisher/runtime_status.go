package publisher

import (
	"time"

	"github.com/google/uuid"
)

type RuntimeState string

const (
	RuntimeStateStopped  RuntimeState = "stopped"
	RuntimeStateStarting RuntimeState = "starting"
	RuntimeStateRunning  RuntimeState = "running"
	RuntimeStateStopping RuntimeState = "stopping"
	RuntimeStateError    RuntimeState = "error"
)

type SourceRuntimeStatus struct {
	Alias           string          `json:"alias"`
	Reference       SourceReference `json:"reference"`
	Available       bool            `json:"available"`
	Quality         SourceQuality   `json:"quality"`
	Sequence        uint64          `json:"sequence,omitempty"`
	ObservedAt      *time.Time      `json:"observed_at,omitempty"`
	PeriodStart     *time.Time      `json:"period_start,omitempty"`
	PeriodEnd       *time.Time      `json:"period_end,omitempty"`
	CoveragePercent *float64        `json:"coverage_percent,omitempty"`
}

type RuntimeStatus struct {
	PublisherID           uuid.UUID             `json:"publisher_id"`
	Type                  Type                  `json:"type"`
	State                 RuntimeState          `json:"state"`
	ConfigVersion         uint                  `json:"config_version"`
	StartedAt             *time.Time            `json:"started_at,omitempty"`
	LastTransitionAt      time.Time             `json:"last_transition_at"`
	LastRequestAt         *time.Time            `json:"last_request_at,omitempty"`
	LastPublishAt         *time.Time            `json:"last_publish_at,omitempty"`
	RequestCount          uint64                `json:"request_count"`
	PublishCount          uint64                `json:"publish_count"`
	FailureCount          uint64                `json:"failure_count"`
	QueueDepth            uint64                `json:"queue_depth"`
	DropCount             uint64                `json:"drop_count"`
	ReconnectCount        uint64                `json:"reconnect_count"`
	Connected             bool                  `json:"connected"`
	ConnectionCount       uint64                `json:"connection_count"`
	DeliveryCount         uint64                `json:"delivery_count"`
	DeliveryFailureCount  uint64                `json:"delivery_failure_count"`
	TransportQueueDepth   uint64                `json:"transport_queue_depth"`
	TransportDropCount    uint64                `json:"transport_drop_count"`
	DiagnosticCount       uint64                `json:"diagnostic_count"`
	DiagnosticDropCount   uint64                `json:"diagnostic_drop_count"`
	ExternalRequestCount  uint64                `json:"external_request_count"`
	RejectedRequestCount  uint64                `json:"rejected_request_count"`
	ActiveConnections     uint64                `json:"active_connections"`
	LastExternalRequestAt *time.Time            `json:"last_external_request_at,omitempty"`
	LastConnectedAt       *time.Time            `json:"last_connected_at,omitempty"`
	LastDeliveredAt       *time.Time            `json:"last_delivered_at,omitempty"`
	LastDiagnosticAt      *time.Time            `json:"last_diagnostic_at,omitempty"`
	TransportError        string                `json:"transport_error,omitempty"`
	Sources               []SourceRuntimeStatus `json:"sources,omitempty"`
	LastError             string                `json:"last_error,omitempty"`
}

func clonePublisherRuntimeStatus(status RuntimeStatus) RuntimeStatus {
	status.StartedAt = cloneTime(status.StartedAt)
	status.LastRequestAt = cloneTime(status.LastRequestAt)
	status.LastPublishAt = cloneTime(status.LastPublishAt)
	status.LastExternalRequestAt = cloneTime(status.LastExternalRequestAt)
	status.LastConnectedAt = cloneTime(status.LastConnectedAt)
	status.LastDeliveredAt = cloneTime(status.LastDeliveredAt)
	status.LastDiagnosticAt = cloneTime(status.LastDiagnosticAt)
	sources := status.Sources
	status.Sources = make([]SourceRuntimeStatus, len(sources))
	for index, source := range sources {
		status.Sources[index] = source
		status.Sources[index].ObservedAt = cloneTime(source.ObservedAt)
		status.Sources[index].PeriodStart = cloneTime(source.PeriodStart)
		status.Sources[index].PeriodEnd = cloneTime(source.PeriodEnd)
		status.Sources[index].CoveragePercent = cloneFloat(source.CoveragePercent)
	}
	return status
}
