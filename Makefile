# Go caches live on the external drive when it is mounted, so the main drive does not fill up.
# Elsewhere the Go defaults apply. Any of these can be overridden from the environment.
HAZEL_CACHE := /Volumes/Hazel/caches
ifneq ($(wildcard $(HAZEL_CACHE)),)
GOMODCACHE ?= $(HAZEL_CACHE)/go
GOCACHE ?= $(HAZEL_CACHE)/go-build
GOPATH ?= $(HAZEL_CACHE)/gopath
export GOMODCACHE GOCACHE GOPATH
endif

ACTION ?= up

# Embedded Postgres runs initdb, which rejects data directories whose permissions
# cannot be set to 0700. exFAT (and msdos) volumes have no POSIX permissions, so
# when .tmp lives on one of them the test data goes on a
# sparse APFS disk image mounted at .tmp/pgvol instead. Other filesystems keep
# the pgtest default (<module root>/.tmp/embedded-pg).
PG_IMG        := $(CURDIR)/.tmp/pg.sparseimage
PG_VOL        := $(CURDIR)/.tmp/pgvol
# Filesystem type of the volume holding this directory, from the longest matching mount point.
PG_FSTYPE     := $(shell mount | awk -v d='$(CURDIR)' '{ mp=$$3; if (mp=="/" || d==mp || index(d, mp "/")==1) { if (length(mp)>best) { best=length(mp); t=substr($$4,2); sub(",.*","",t); fs=t } } } END { print fs }')
ifneq ($(filter exfat msdos,$(PG_FSTYPE)),)
export TENANTLY_EMBEDDED_PG_DIR := $(PG_VOL)
PG_NEEDS_VOL := pgvol
endif

.PHONY: up down build vet test test-store test-rls migrate gqlgen k6 k6-report pgvol pgvol-detach

# go test kills the binary after this long. Embedded Postgres first-run initdb
# on the external drive can take many minutes, so the 10m default is too short.
TEST_TIMEOUT ?= 30m

# One command: start Postgres and Redis, migrate, enable the app role, run the API.
# Needs Docker with Compose v2 and a .env copied from .env.example.
up:
	@[ -f .env ] || { echo ".env missing: cp .env.example .env and replace every changeme value"; exit 1; }
	docker compose up -d --wait
	set -a; . ./.env; set +a; go run ./cmd/migrate up
	docker compose exec -T postgres sh /docker-entrypoint-initdb.d/01-app-role.sh
	set -a; . ./.env; set +a; go run ./cmd/api

# Stops the containers. Add -v manually to also drop the Postgres volume.
down:
	docker compose down

build:
	go build ./...

vet:
	go vet ./...

# Attaches the APFS volume when the data directory needs one. No-op otherwise.
pgvol:
ifneq ($(filter exfat msdos,$(PG_FSTYPE)),)
	mkdir -p $(CURDIR)/.tmp $(PG_VOL)
	@if ! mount | grep -q " on $(PG_VOL) "; then \
		[ -f $(PG_IMG) ] || hdiutil create -size 2g -type SPARSE -fs APFS -volname tenantly-pg $(PG_IMG); \
		hdiutil attach -nobrowse -mountpoint $(PG_VOL) $(PG_IMG); \
	fi
endif

# Teardown: detaches the volume if it is mounted. The image file is kept for reuse.
pgvol-detach:
ifneq ($(filter exfat msdos,$(PG_FSTYPE)),)
	@if mount | grep -q " on $(PG_VOL) "; then hdiutil detach $(PG_VOL) || hdiutil detach -force $(PG_VOL); fi
endif

test: $(PG_NEEDS_VOL)
	go test -p 1 -timeout $(TEST_TIMEOUT) ./...

# Fails on any skip, so a missing database cannot pass silently.
# Requires embedded Postgres or TENANTLY_TEST_DATABASE_URL.
test-store: $(PG_NEEDS_VOL)
	TENANTLY_REQUIRE_DB=1 go test -count=1 -p 1 -timeout $(TEST_TIMEOUT) -v ./internal/store/... ./internal/db/...; rc=$$?; $(MAKE) --no-print-directory pgvol-detach; exit $$rc

# Fails on any skip. Requires embedded Postgres or TENANTLY_TEST_DATABASE_URL.
test-rls: $(PG_NEEDS_VOL)
	TENANTLY_REQUIRE_DB=1 go test -count=1 -p 1 -timeout $(TEST_TIMEOUT) -run RLS -v ./internal/store/...; rc=$$?; $(MAKE) --no-print-directory pgvol-detach; exit $$rc

# Usage: make migrate ACTION=up|down|status (uses MIGRATE_DATABASE_URL)
migrate:
	go run ./cmd/migrate $(ACTION)

gqlgen:
	go run github.com/99designs/gqlgen generate

# Runs the load test. Needs k6 on PATH and a running API with APP_ENV=dev or test.
# Writes loadtest/results/summary.json.
k6:
	@command -v k6 >/dev/null 2>&1 || { echo "k6 is not installed: see https://grafana.com/docs/k6/latest/set-up/install-k6/"; exit 1; }
	k6 run loadtest/k6/tenantly.js

# Prints the Markdown results table from loadtest/results/summary.json to stdout.
k6-report:
	go run ./cmd/k6report -in loadtest/results/summary.json
