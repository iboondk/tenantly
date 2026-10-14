-- +goose Up
-- Login runs before any tenant context exists, so it cannot go through RLS.
-- auth_upsert_user is the only path that creates users or identities for the
-- app role. It is SECURITY DEFINER, so it runs with the owner's privileges, and
-- search_path is pinned to avoid search_path hijacking. The owner must be a
-- superuser or have BYPASSRLS; otherwise FORCE RLS would block its writes.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION auth_upsert_user(
    p_provider text,
    p_subject  text,
    p_email    text,
    p_name     text
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_user_id uuid;
    v_email   text := lower(p_email);
BEGIN
    -- 1. Known identity: return its user.
    SELECT ui.user_id INTO v_user_id
    FROM user_identities ui
    WHERE ui.provider = p_provider AND ui.subject = p_subject;
    IF FOUND THEN
        RETURN v_user_id;
    END IF;

    IF v_email IS NULL OR v_email = '' THEN
        RAISE EXCEPTION 'auth_upsert_user: email is required for a new identity'
            USING ERRCODE = '22023';
    END IF;

    -- 2. Existing user with the same email, otherwise create one.
    SELECT u.id INTO v_user_id
    FROM users u
    WHERE lower(u.email) = v_email
    LIMIT 1;

    IF v_user_id IS NULL THEN
        INSERT INTO users (email, display_name)
        VALUES (v_email, COALESCE(p_name, ''))
        ON CONFLICT (email) DO NOTHING
        RETURNING id INTO v_user_id;

        IF v_user_id IS NULL THEN
            -- A concurrent insert won the race. Read its row.
            SELECT u.id INTO v_user_id
            FROM users u
            WHERE lower(u.email) = v_email
            LIMIT 1;
        END IF;
    END IF;

    -- 3. Link the identity. A concurrent login may have linked it first, so
    --    read back the stored user_id instead of trusting v_user_id.
    INSERT INTO user_identities (provider, subject, user_id)
    VALUES (p_provider, p_subject, v_user_id)
    ON CONFLICT (provider, subject) DO NOTHING;

    SELECT ui.user_id INTO v_user_id
    FROM user_identities ui
    WHERE ui.provider = p_provider AND ui.subject = p_subject;

    RETURN v_user_id;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION auth_upsert_user(text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_upsert_user(text, text, text, text) TO tenantly_app;

-- +goose Down
DROP FUNCTION IF EXISTS auth_upsert_user(text, text, text, text);
