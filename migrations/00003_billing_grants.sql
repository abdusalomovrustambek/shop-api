-- +goose Up
-- billing-worker marks orders paid or failed. The role exists only on the deployed stack.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'billing') THEN
        GRANT USAGE ON SCHEMA shop TO billing;
        GRANT SELECT, UPDATE ON shop.orders TO billing;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'billing') THEN
        REVOKE SELECT, UPDATE ON shop.orders FROM billing;
    END IF;
END
$$;
-- +goose StatementEnd
