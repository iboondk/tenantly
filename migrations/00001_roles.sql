-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tenantly_app') THEN
        CREATE ROLE tenantly_app NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO tenantly_app;

-- +goose Down
-- Roles are cluster-global and can be shared with other databases, so this
-- migration deliberately drops nothing.
