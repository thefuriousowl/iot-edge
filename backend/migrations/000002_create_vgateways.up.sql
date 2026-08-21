BEGIN;

CREATE TABLE vgateways (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) UNIQUE NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type = 'modbus_tcp'),
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_vgateways_type
    ON vgateways(type);

CREATE INDEX idx_vgateways_enabled
    ON vgateways(enabled);

CREATE TABLE vgateway_stats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vgateway_id UUID NOT NULL REFERENCES vgateways(id) ON DELETE CASCADE,
    connected_at TIMESTAMP WITH TIME ZONE,
    disconnected_at TIMESTAMP WITH TIME ZONE,
    request_count BIGINT NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    error_count BIGINT NOT NULL DEFAULT 0 CHECK (error_count >= 0),
    bytes_received BIGINT NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
    avg_latency_ms DECIMAL(10, 2) CHECK (avg_latency_ms >= 0),
    recorded_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_vgateway_stats_vgateway
    ON vgateway_stats(vgateway_id);

CREATE INDEX idx_vgateway_stats_recorded
    ON vgateway_stats(recorded_at);

COMMIT;
