BEGIN;

CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    datasource_id UUID REFERENCES datasources(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) NOT NULL,
    data_type VARCHAR(20) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT tags_name_key UNIQUE (name),
    CONSTRAINT tags_type_check CHECK (type IN ('reading', 'constant', 'calculated')),
    CONSTRAINT tags_data_type_check CHECK (data_type IN ('bool', 'int16', 'uint16', 'int32', 'uint32', 'float32', 'float64')),
    CONSTRAINT tags_datasource_scope_check CHECK (
        (type = 'reading' AND datasource_id IS NOT NULL)
        OR (type IN ('constant', 'calculated') AND datasource_id IS NULL)
    )
);

CREATE INDEX idx_tags_datasource ON tags(datasource_id);
CREATE INDEX idx_tags_type ON tags(type);
CREATE INDEX idx_tags_enabled ON tags(enabled);

CREATE TABLE tag_dependencies (
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    depends_on_tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (tag_id, depends_on_tag_id),
    CONSTRAINT tag_dependencies_not_self CHECK (tag_id <> depends_on_tag_id)
);

CREATE INDEX idx_tag_dependencies_dependency ON tag_dependencies(depends_on_tag_id);

COMMIT;
