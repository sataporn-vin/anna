# Anna Personal Memory Service

Anna is a single-user personal-memory backend. Version one provides:

- authenticated REST endpoints;
- constrained MongoDB reads, writes, and aggregations for flexible collections;
- managed finance and reminder collections with stricter validation;
- a managed event/life-log collection with guarded custom attributes;
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

Generic writes to all managed collections are rejected. Generic reads and safe aggregations remain available.

## Event / life log

Record a completed event that is worth remembering but is not itself a financial transaction:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "requestId":"d19f46f2-f760-4764-89ad-a22ae819ce6e",
    "occurredOn":"2026-08-19",
    "timezone":"Asia/Bangkok",
    "title":"Company monthly dinner at Sushiro",
    "eventType":"company-event",
    "location":"Sushiro Theprak branch",
    "tags":["company","dinner","sushiro"],
    "financialContext":{
      "currency":"THB",
      "totalValueMinor":45100,
      "allowanceMinor":50000,
      "coveredByOthersMinor":45100,
      "personalPaymentMinor":0
    },
    "attributes":{"restaurantChain":"Sushiro"},
    "rawText":"Company monthly dinner at Sushiro. The company paid the full bill."
  }' \
  http://localhost:8080/v1/events
```

The amounts under `financialContext` are descriptive and never affect transaction totals or account balances. If the user also paid personal money, create the transaction first and include its returned object identifier in `relatedTransactionIds`.

Search uses normalized exact fields and tokens; it is not fuzzy or semantic search:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"text":"sushiro","sort":"occurred_desc","limit":1}' \
  http://localhost:8080/v1/events/search
```

`attributes` accepts up to 32 event-specific fields containing short JSON scalars or scalar arrays. Core event fields remain strictly validated, and arbitrary attributes are not automatically indexed.

## Recurring reminders

Create a reminder rule for wearing a company uniform every Monday and Friday. Its preparation step starts two days before each occurrence:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id":"company-uniform",
    "title":"Wear the company uniform",
    "timezone":"Asia/Bangkok",
    "weekdays":["monday","friday"],
    "startsOn":"2026-08-17",
    "preparation":{"title":"Wash the company uniform","leadDays":2}
  }' \
  http://localhost:8080/v1/reminders
```

Request a digest for a local calendar date. Omitting `on` uses today's date in `DEFAULT_TIMEZONE`:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  'http://localhost:8080/v1/reminders/digest?on=2026-08-19'
```

The preparation item identifies the uniform occurrence it belongs to. Marking that preparation complete suppresses its later repeat:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $ANNA_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"reminderId":"company-uniform","occurrenceOn":"2026-08-21","phase":"preparation"}' \
  http://localhost:8080/v1/reminders/completions
```

Use phase `occurrence` to complete the wear reminder itself. Completions apply only to the specified occurrence; the recurring rule remains active. This is a pull-based digest, not a scheduled push notification.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health/live` | Process liveness; public |
| `GET` | `/health/ready` | MongoDB readiness; public and minimal |
| `GET` | `/v1/collections` | List accessible collections |
| `POST` | `/v1/collections` | Create a flexible collection |
| `POST` | `/v1/accounts` | Create a managed account |
| `POST` | `/v1/transactions` | Create an idempotent, validated transaction |
| `POST` | `/v1/events` | Create an idempotent, validated completed event |
| `GET` | `/v1/events/{id}` | Retrieve one event |
| `POST` | `/v1/events/search` | Search events with structured filters |
| `PATCH` | `/v1/events/{id}` | Correct mutable event fields |
| `DELETE` | `/v1/events/{id}` | Delete an event without changing linked transactions |
| `POST` | `/v1/reminders` | Create a managed recurring reminder rule |
| `GET` | `/v1/reminders/digest` | Retrieve incomplete reminders for a local date |
| `POST` | `/v1/reminders/completions` | Complete one phase of one reminder occurrence |
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

The MongoDB integration test creates and removes a uniquely named temporary database when `MONGODB_TEST_URI` is set:

```sh
cd /Users/sataporn.vin/bag/codex/anna
MONGODB_TEST_URI='mongodb://localhost:27017' go test ./internal/mongostore -run TestManagedEventsIntegration
```

An existing unvalidated `events` collection requires a one-time `collMod` upgrade. Startup rejects incompatible legacy event documents. The database user performing that upgrade needs `collMod` permission; newly created databases receive the validator during bootstrap and do not need the extra permission.
