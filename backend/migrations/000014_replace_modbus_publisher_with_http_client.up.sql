DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM data_publishers WHERE type = 'modbus_tcp_server') THEN
        RAISE EXCEPTION 'remove obsolete modbus_tcp_server Publishers before applying migration 000014' USING ERRCODE = '23514';
    END IF;
END
$$;

ALTER TABLE data_publishers
    DROP CONSTRAINT data_publishers_type_check,
    ADD CONSTRAINT data_publishers_type_check CHECK (type IN ('http_server', 'mqtt', 'http_client'));

ALTER TABLE credential_profiles
    DROP CONSTRAINT credential_profiles_type_check,
    ADD CONSTRAINT credential_profiles_type_check CHECK (type IN ('mqtt', 'http'));

ALTER TABLE credential_secrets
    DROP CONSTRAINT credential_secrets_name_check,
    DROP CONSTRAINT credential_secrets_kind_check,
    ADD CONSTRAINT credential_secrets_name_check CHECK (name IN (
        'mqtt.username', 'mqtt.password', 'mqtt.custom_ca', 'mqtt.client_identity',
        'http.username', 'http.password', 'http.api_key', 'http.bearer_token',
        'http.oauth_client_secret', 'http.custom_ca', 'http.client_identity'
    )),
    ADD CONSTRAINT credential_secrets_kind_check CHECK (
        (name IN (
            'mqtt.username', 'mqtt.password',
            'http.username', 'http.password', 'http.api_key', 'http.bearer_token', 'http.oauth_client_secret'
        ) AND kind = 'opaque')
        OR (name IN ('mqtt.custom_ca', 'http.custom_ca') AND kind = 'ca_certificate')
        OR (name IN ('mqtt.client_identity', 'http.client_identity') AND kind = 'client_identity')
    );
