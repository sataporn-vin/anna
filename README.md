# Anna Personal Memory Service

Anna is a single-user personal-memory backend. Version one provides:

- authenticated REST endpoints;
- constrained MongoDB reads, writes, and aggregations for flexible collections;
- managed `accounts` and `transactions` collections with stricter validation;
- integer minor-unit money;
- split `occurredOn` and optional `occurredAt` date semantics;
- portable Docker deployment for local use, Railway, and later self-hosting.

The bulk expense-memo parser, aliases, MCP, semantic search, and multi-user sharing are not implemented yet.

## Local startup

Requirements: Docker with Compose support.

```sh
cd /Users/sataporn.vin/bag/codex/anna
cp .env.example .env
```

Replace every placeholder secret in `/Users/sataporn.vin/bag/codex/anna/.env`. Use URL-safe alphanumeric values for the MongoDB passwords because Compose places the application password in a MongoDB URI.

Then start both containers:

```sh
cd /Users/sataporn.vin/bag/codex/anna
docker compose up --build
```

Check readiness:

```sh
curl http://localhost:8080/health/ready
```

## Authentication

Every `/v1` endpoint requires the configured bearer token:

```sh
export ANNA_TOKEN='the-value-from-AUTH_BEARER_TOKEN'
curl -H "Authorization: Bearer $ANNA_TOKEN" http://localhost:8080/v1/collections
```

Do not put the token directly into shell history on a shared machine.

## First transaction

Create an account:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":"kbank-visa","name":"KBank Visa","kind":"credit_card","currency":"THB"}' \
  http://localhost:8080/v1/accounts
```

Create a transaction. Generate a new `requestId` for each logical transaction and reuse it only when retrying that same request:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "requestId":"9c3e06d5-34c0-4fc5-88cd-dd68a4db7a64",
    "occurredOn":"2026-08-15",
    "timezone":"Asia/Bangkok",
    "amountMinor":48500,
    "currency":"THB",
    "transactionKind":"expense",
    "accountId":"kbank-visa",
    "descriptorRaw":"Fuji",
    "merchantName":"Fuji",
    "categoryPath":["food"],
    "rawText":"Lunch at Fuji, 485 baht, paid with KBank Visa."
  }' \
  http://localhost:8080/v1/transactions
```

`occurredAt` is omitted because the example has no actual time. If supplied, it must be an RFC 3339 instant whose local date in `timezone` equals `occurredOn`.

## Flexible collections

Create a collection:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"projects"}' \
  http://localhost:8080/v1/collections
```

Insert a document:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"collection":"projects","document":{"name":"Anna","status":"active"}}' \
  http://localhost:8080/v1/mongo/insert-one
```

Generic writes to `accounts` and `transactions` are rejected. Generic reads and safe aggregations remain available.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health/live` | Process liveness; public |
| `GET` | `/health/ready` | MongoDB readiness; public and minimal |
| `GET` | `/v1/collections` | List accessible collections |
| `POST` | `/v1/collections` | Create a flexible collection |
| `POST` | `/v1/accounts` | Create a managed account |
| `POST` | `/v1/transactions` | Create an idempotent, validated transaction |
| `POST` | `/v1/mongo/find` | Find documents |
| `POST` | `/v1/mongo/find-one` | Find one document |
| `POST` | `/v1/mongo/insert-one` | Insert into a flexible collection |
| `POST` | `/v1/mongo/update-one` | Update one flexible document |
| `POST` | `/v1/mongo/delete-one` | Delete one flexible document |
| `POST` | `/v1/mongo/aggregate` | Run an allowlisted aggregation |

MongoDB-specific request values use MongoDB Extended JSON. Filters allow `$and`, `$or`, `$nor`, `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$in`, `$nin`, and `$exists`. Aggregation is restricted to the stages and expressions documented in `memo.md`.

## Railway deployment

1. Create a Railway project and add its MongoDB template.
2. Add this repository as a second service and generate a public domain for it.
3. In the application service, configure:

```text
MONGODB_URI=${{MongoDB.MONGO_URL}}
MONGODB_DATABASE=personal_memory
AUTH_BEARER_TOKEN=<at least 32 random characters>
DEFAULT_TIMEZONE=Asia/Bangkok
```

If the database service has a different name, replace `MongoDB` in the reference variable. The remaining limits have safe defaults. Railway supplies `PORT`; the application listens on it automatically.

`railway.json` selects the root Dockerfile and configures `/health/ready` as the deployment health check. The application exits when MongoDB is unavailable at startup; Railway's restart policy retries it while the database starts.

For initial testing, `MONGO_URL` uses the template's database credentials. Before treating the deployment as production, create a dedicated `readWrite` MongoDB user for `personal_memory`, change `MONGODB_URI` to that user, remove public MongoDB TCP access if it is not needed, and configure backups.

Current platform references:

- [Railway MongoDB](https://docs.railway.com/databases/mongodb)
- [Railway config as code](https://docs.railway.com/config-as-code/reference)
- [Railway Dockerfiles](https://docs.railway.com/builds/dockerfiles)

## Verification

```sh
cd /Users/sataporn.vin/bag/codex/anna
gofmt -w cmd internal
go vet ./...
go test ./...
docker compose config
docker build -t anna-memory-api:test .
```
