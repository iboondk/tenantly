-- +goose Up
-- Every tenant-owned table gets ENABLE and FORCE, so the table owner is also
-- subject to policies. current_setting(..., true) returns NULL when the
-- setting is missing, and NULLIF turns an empty string into NULL, so a
-- request without tenant context sees zero rows and never an error.

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenants_isolation ON tenants
    USING (id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY memberships_isolation ON memberships
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE projects FORCE ROW LEVEL SECURITY;
CREATE POLICY projects_isolation ON projects
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- users: a row is visible when it is the current user, or when it belongs to a
-- member of the current tenant. The EXISTS subquery is itself filtered by the
-- memberships policy above, so it only sees the current tenant's memberships.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY users_select ON users
    FOR SELECT
    USING (
        id = NULLIF(current_setting('app.user_id', true), '')::uuid
        OR EXISTS (
            SELECT 1 FROM memberships m
            WHERE m.user_id = users.id
        )
    );

-- user_identities: no policy, so with FORCE the table denies everything to
-- non-owner roles. The app role is also revoked, and only the SECURITY DEFINER
-- function in 00004 touches the table.
ALTER TABLE user_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_identities FORCE ROW LEVEL SECURITY;

GRANT SELECT, INSERT, UPDATE, DELETE ON tenants     TO tenantly_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO tenantly_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON projects    TO tenantly_app;
GRANT SELECT ON users TO tenantly_app;
REVOKE ALL ON user_identities FROM tenantly_app;

-- +goose Down
REVOKE ALL ON tenants, memberships, projects, users FROM tenantly_app;

DROP POLICY IF EXISTS users_select ON users;
DROP POLICY IF EXISTS projects_isolation ON projects;
DROP POLICY IF EXISTS memberships_isolation ON memberships;
DROP POLICY IF EXISTS tenants_isolation ON tenants;

ALTER TABLE user_identities NO FORCE ROW LEVEL SECURITY;
ALTER TABLE user_identities DISABLE ROW LEVEL SECURITY;
ALTER TABLE users NO FORCE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
ALTER TABLE projects NO FORCE ROW LEVEL SECURITY;
ALTER TABLE projects DISABLE ROW LEVEL SECURITY;
ALTER TABLE memberships NO FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships DISABLE ROW LEVEL SECURITY;
ALTER TABLE tenants NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenants DISABLE ROW LEVEL SECURITY;
