# Demo script: recording a GIF or Loom

This script shows the one claim that matters most: a tenant cannot read another tenant's data, and the database enforces that even when the application code is wrong. The whole demo takes about 90 seconds of screen time.

Use dummy data only. Every email and name below is a placeholder, and `.env` must come from `.env.example` with its `changeme` values replaced.

## Before you hit record

1. Use a terminal with a large font (16pt or more) and a dark theme. Set the window to about 1280 x 720 so the GIF stays small.
2. Clear the screen and set a short prompt, for example `export PS1='$ '`.
3. Start the infrastructure and the API in a separate, muted pane, so the recording shows only the demo commands:

   ```sh
   cp .env.example .env            # then replace every changeme value
   make up                         # Compose, migrations, app role, API; keep this pane running
   ```

4. In the recording pane, export the base URL and check the health route:

   ```sh
   export BASE=http://localhost:8080
   curl -s $BASE/healthz
   ```

   Expected: `{"status":"ok"}`. If this fails, stop and fix the API before recording.

5. Install `jq` if it is not already available: `brew install jq`.

## Scene 1: Two tenants, two users (about 15 s)

Say: "Alice and Bob each get a token. Alice creates tenant Acme, and Bob creates tenant Globex."

```sh
ALICE=$(curl -s -X POST $BASE/v1/auth/dev-token -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.test"}' | jq -r .token)
BOB=$(curl -s -X POST $BASE/v1/auth/dev-token -H 'Content-Type: application/json' \
  -d '{"email":"bob@example.test"}' | jq -r .token)

ACME=$(curl -s -X POST $BASE/v1/tenants -H "Authorization: Bearer $ALICE" \
  -H 'Content-Type: application/json' -d '{"name":"Acme"}' | jq -r .id)
GLOBEX=$(curl -s -X POST $BASE/v1/tenants -H "Authorization: Bearer $BOB" \
  -H 'Content-Type: application/json' -d '{"name":"Globex"}' | jq -r .id)
echo "Acme=$ACME Globex=$GLOBEX"
```

Show: both IDs print. Do not zoom in on the token values.

## Scene 2: Each tenant creates a project (about 15 s)

Say: "Owners can create projects in their own tenant."

```sh
curl -s -X POST $BASE/v1/tenants/$ACME/projects -H "Authorization: Bearer $ALICE" \
  -H 'Content-Type: application/json' -d '{"name":"Acme launch"}' | jq
curl -s -X POST $BASE/v1/tenants/$GLOBEX/projects -H "Authorization: Bearer $BOB" \
  -H 'Content-Type: application/json' -d '{"name":"Globex roadmap"}' | jq
```

Show: each call returns `201`-style JSON with the project `name`. Pause for one second after each so the viewer can read it.

## Scene 3: Members see only their own tenant (about 15 s)

Say: "Alice lists her tenant's projects. She sees only Acme's project."

```sh
curl -s $BASE/v1/tenants/$ACME/projects -H "Authorization: Bearer $ALICE" | jq
```

Show: the list contains `Acme launch` and nothing from Globex.

## Scene 4: The cross-tenant request is denied (about 20 s)

This is the key moment. Say: "Alice asks for Globex's project list. She is not a member of Globex, so the API returns 404 and does not reveal that the tenant exists."

```sh
curl -s -w '\nHTTP %{http_code}\n' $BASE/v1/tenants/$GLOBEX/projects \
  -H "Authorization: Bearer $ALICE"
```

Expected: a JSON body with `"error":"not_found"` and `HTTP 404`.

Then show the database layer. Say: "Even if a handler skipped the membership check, Postgres would still return no rows, because the app role runs under row-level security."

```sh
docker compose exec -T postgres psql -U postgres -d tenantly -c \
  "SET ROLE tenantly_app; SELECT count(*) AS visible_without_tenant FROM projects;"
```

Expected: `visible_without_tenant` is `0`. Note: `SET ROLE` needs a superuser session, which the `postgres` user is. If the query errors on your Postgres setup, leave this scene out and say so in the voiceover instead of editing the output.

## Scene 5: Role check and rate limit (optional, about 15 s)

Say: "A plain member cannot create projects. The API returns 403 for a role that is too low."

Only record this scene if a member account exists in the tenant. Otherwise skip it, because the demo would need a second invite flow that the API does not have yet.

For the rate limit, say: "Requests are limited per user and tenant. Over the limit the API returns 429 with a Retry-After header." Show it only with `RATE_LIMIT_PER_MINUTE` set low (for example `RATE_LIMIT_PER_MINUTE=5`) and a short loop, and only if the output is what you see on screen.

## Scene 6: Close (about 10 s)

Say: "Tenant isolation is in the database, so a handler bug cannot leak rows. The tests in `make test-rls` check this on every push."

Show the terminal running:

```sh
make test-rls
```

Let it finish and show the `PASS` lines. Do not cut away from a failing run. If a test fails, re-record instead of editing the result.

## Recording tips

- Record at 10 to 15 frames per second for a GIF, and trim the first and last second.
- Convert with `ffmpeg -i demo.mov -vf "fps=12,scale=960:-1:flags=lanczos" demo.gif`, then check the file size. Keep it under 10 MB for GitHub.
- For Loom, record the full screen. Keep the webcam off, because the demo is purely terminal output.
- Never show a real token or a real `.env`. Redact with a blur or re-record if one appears on screen.
- Speak the expected result before you run each command, then let the output confirm it.

## Shot list summary

| Scene | Command | What the viewer should see |
|---|---|---|
| 0 | `curl $BASE/healthz` | `{"status":"ok"}` |
| 1 | dev-token and tenant create | Two tenant IDs |
| 2 | project create (both tenants) | Project JSON for each tenant |
| 3 | list own tenant | Only Acme's project |
| 4 | list Globex as Alice | `404` with `not_found`, and zero rows from SQL |
| 5 | optional role and rate-limit check | `403`, or `429` with `Retry-After` |
| 6 | `make test-rls` | `PASS` for every RLS test |
