BEGIN;

CREATE TABLE devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vgateway_id UUID NOT NULL REFERENCES vgateways(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(50) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT devices_vgateway_name_key UNIQUE (vgateway_id, name)
);

CREATE INDEX idx_devices_vgateway ON devices(vgateway_id);
CREATE INDEX idx_devices_type ON devices(type);

CREATE TABLE datasources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(50) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT datasources_device_name_key UNIQUE (device_id, name)
);

CREATE INDEX idx_datasources_device ON datasources(device_id);
CREATE INDEX idx_datasources_type ON datasources(type);

COMMIT;
