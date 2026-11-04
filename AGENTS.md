# shop-api — instructions for coding agents

shop-api is the Go HTTP API of the Shop demo: catalog, cart, coupons, orders and checkout.
Go 1.26, PostgreSQL, only `pgx` and `goose` as dependencies besides the standard library.

## Build, lint, test

```
make build        # go build of cmd/shop-api and cmd/shopper into bin/
make lint         # golangci-lint run
make test         # go test -race ./...
go vet ./...
```

Database tests need `SHOP_TEST_DATABASE_URL`: a `postgres://` URL (user, host, port, database, `sslmode=disable` for a local
server) of a PostgreSQL server whose role has CREATEDB. Each test gets its own freshly migrated database.
Run `gofmt -w .` before you finish.

## Layout

- `cmd/shop-api` — config from env, migrations, pool, server lifecycle. No business logic.
- `cmd/shopper` — traffic generator for the demo stack (`-dry-run` prints the requests).
- `internal/httpx` — router, JSON helpers, error mapping, middleware. No SQL.
- `internal/auth` — JWT verification against auth-gateway's JWKS.
- `internal/catalog`, `cart`, `coupon`, `order` — one package per feature: `repo.go` (SQL) and `handler.go` (HTTP).
  The coupon rules are pure functions in `coupon/coupon.go`.
- `internal/server` — registers every route; used by the binary and by tests.
- `migrations/` — goose SQL migrations of schema `shop`, embedded and applied on start.
- `api/openapi.yaml` — the OpenAPI 3.1 spec, served at `/api/openapi.yaml`; Swagger UI at `/api/docs`.
- `deploy/` — docker-compose for the whole stack, role SQL, Caddy snippet.

## Rules

- **Money is integer cents** (`int64`, `BIGINT`), never floats. Percent discounts round half up.
- **Parameterized SQL.** Pass values as `$1, $2, ...` arguments.
- **Update `api/openapi.yaml` with every API change.** A test compares the registered routes with the spec.
  When a JSON shape changes, shop-web's `src/api/types.ts` must follow.
- **Migrations are append-only.** A change is a new numbered file in `migrations/` with `Up` and `Down`. Never edit an applied one.
- Do not log secrets or card data.
- New behaviour comes with a test in the style of its neighbours.

## Errors and statuses

Handlers return errors wrapped with the sentinels in `internal/httpx/errors.go` (`httpx.Errorf(kind, ...)`) and answer with
`httpx.WriteError` or `httpx.Fail`. The body is always `{"error": {"code": "...", "message": "..."}}`.

| Sentinel | Status | Code |
|---|---|---|
| `ErrInvalid` | 400 | `invalid_request` |
| `ErrUnauthorized` | 401 | `unauthorized` |
| `ErrNotFound` | 404 | `not_found` |
| `ErrConflict` | 409 | `conflict` |
| `ErrUnprocessable` | 422 | `unprocessable` |
| `ErrUnavailable` | 503 | `unavailable` |
| anything else | 500 | `internal` (generic message) |

`httpx.Fail` also logs server errors at `error` level with `err`, `caller` and the inputs of the failing call.

## The `billing.jobs` contract

billing-worker owns the table `billing.jobs` (its migrations create it). shop-api only inserts, in the same transaction as the order:

```sql
INSERT INTO billing.jobs (kind, payload) VALUES ('charge_order', $1)
```

| Column | Type | Notes |
|---|---|---|
| `id` | bigserial | |
| `kind` | text | `charge_order` |
| `payload` | jsonb | `{"order_id": 1, "user_id": "<uuid>", "currency": "USD", "total_cents": 3600}` (never card data: no PAN, expiry or CVC) |
| `status` | text | defaults to `queued` |
| `created_at` | timestamptz | defaults to `now()` |

Both repositories document this table; change it in both. billing-worker marks `shop.orders` as `paid` or `failed`.

## Database roles and migrations

Migrations run on start as the role `shop_api`, which owns schema `shop` but is not a superuser. goose keeps its version table
in `shop.goose_db_version`. `deploy/roles.sql` creates the roles and schemas; `testdb` creates the same schemas and a minimal
`billing.jobs` for tests.

## Deploy

The stack runs as compose project `shop` in `/opt/shop` (`deploy/docker-compose.yml`). Only `auth-gateway` joins the external
network `agnts_default`. Settings come from `/opt/shop/.env` (never commit it), with these variables, all throwaway values:

- `POSTGRES_ADMIN_SECRET` — the postgres superuser's secret; used only to run `roles.sql` and the commands below.
- `SHOP_API_DATABASE_URL`, `BILLING_DATABASE_URL`, `AUTH_DATABASE_URL` — one connection URL per service (scheme `postgres`,
  the service's own role, host `postgres`, port 5432, database `shop`, `sslmode=disable`).
- `SHOP_SRC` — where the repositories are checked out (default `/opt/shop/src`).
- `SHOPPER_INCIDENTS` — empty for no incidents, unset for all (`api-1,api-2,api-3,zero-qty,uzs,brute,scan`); `SHOPPER_INCIDENT_EVERY` — default `60s`.

Rollout order:

1. `docker compose -p shop up -d postgres`.
2. Apply `roles.sql` as the postgres user, then set the secret of each role (`shop_api`, `billing`, `auth`) with `ALTER ROLE <role> ...` to the one used in its URL.
3. `docker compose -p shop up -d billing-worker` so that it creates `billing.jobs`, then apply `roles.sql` again (it grants shop_api the insert on that table).
4. `docker compose -p shop up -d shop-api auth-gateway`, then `shopper` (start with `SHOPPER_INCIDENTS` empty).

Rollback: `docker compose -p shop down`; add `-v` to drop the database volume.

`make demo-clean` (with `DATABASE_URL` set) deletes orders older than 7 days and restocks products to at least 3000 units.
