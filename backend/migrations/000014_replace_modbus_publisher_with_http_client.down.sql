DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM data_publishers WHERE type = 'http_client') THEN
        RAISE EXCEPTION 'remove http_client Publishers before reverting migration 000014' USING ERRCODE = '23514';
    END IF;
    IF EXISTS (SELECT 1 FROM credential_profiles WHERE type = 'http') THEN
        RAISE EXCEPTION 'remove HTTP Credential Profiles before reverting migration 000014' USING ERRCODE = '23514';
    END IF;
END
$$;

ALTER TABLE credential_secrets
    DROP CONSTRAINT credential_secrets_name_check,
    DROP CONSTRAINT credential_secrets_kind_check,
    ADD CONSTRAINT credential_secrets_name_check CHECK (name IN (
        'mqtt.username', 'mqtt.password', 'mqtt.custom_ca', 'mqtt.client_identity'
    )),
    ADD CONSTRAINT credential_secrets_kind_check CHECK (
        (name IN ('mqtt.username', 'mqtt.password') AND kind = 'opaque')
        OR (name = 'mqtt.custom_ca' AND kind = 'ca_certificate')
        OR (name = 'mqtt.client_identity' AND kind = 'client_identity')
    );

ALTER TABLE credential_profiles
    DROP CONSTRAINT credential_profiles_type_check,
    ADD CONSTRAINT credential_profiles_type_check CHECK (type IN ('mqtt'));

ALTER TABLE data_publishers
    DROP CONSTRAINT data_publishers_type_check,
    ADD CONSTRAINT data_publishers_type_check CHECK (type IN ('http_server', 'mqtt', 'modbus_tcp_server'));
