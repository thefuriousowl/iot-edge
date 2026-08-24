package datalogger

import "time"

type PostgreSQLPhysicalAllocation struct {
	RawHistoryBytes      int64 `json:"raw_history_bytes"`
	BatchAccountingBytes int64 `json:"batch_accounting_bytes"`
	TotalBytes           int64 `json:"total_bytes"`
}

type DataManagementOverview struct {
	EvaluatedAt                  time.Time                    `json:"evaluated_at"`
	LoggerCount                  int64                        `json:"logger_count"`
	EnabledLoggerCount           int64                        `json:"enabled_logger_count"`
	PolicyLoggerCount            int64                        `json:"policy_logger_count"`
	LogicalHistory               RetentionMetrics             `json:"logical_history"`
	PostgreSQLPhysicalAllocation PostgreSQLPhysicalAllocation `json:"postgresql_physical_allocation"`
}
