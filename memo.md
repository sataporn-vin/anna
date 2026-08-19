# Personal Memory Service — Implementation Memo

## Goal

Build a lightweight personal memory backend that ChatGPT can use to store and retrieve structured information over time.

Version one is a **single-user REST service**. MCP support and multi-user sharing are explicitly deferred until the REST deployment has been proven on Railway.

Initial use case: expense tracking.

Longer-term use cases:
- Personal preferences
- People/contact context
- Health measurements
- Fitness records
- Travel information
- Projects
- Events
- Notes and other arbitrary memories

## Hosting

Use the existing **Railway subscription**.

Railway is the initial deployment target, not an application dependency. The application must run from the same Docker image on Railway, through local Docker Compose, or on a future self-hosted machine. All environment-specific behavior must come from configuration.

Deploy both services in one Railway project:

```text
ChatGPT / AI client
        │
        │ HTTPS
        ▼
Memory API / MCP Service
        │
        │ Railway private network
        ▼
MongoDB
```

### Railway services

1. `memory-api`
   - Public HTTPS endpoint
   - Thin application layer
   - Connects to MongoDB over Railway private networking

2. `mongodb`
   - Private service
   - Persistent volume
   - Do not expose MongoDB publicly

## Database Choice

Use **MongoDB**.

Reason:
- The project is intended to become general ChatGPT memory storage, not just an accounting database.
- Different memory types will have different structures.
- MongoDB documents allow flexible schemas.
- MongoDB aggregation can perform filtering and calculations server-side, minimizing tokens sent back to the model.

## Database Structure

Do not put everything into one completely unstructured collection.

Bootstrap these collections:

```text
accounts
payment_channels
transactions
memories
people
events
measurements
documents
reminders
reminder_completions
```

The client may create additional collections through the memory service. Collection creation must follow these rules:

```text
- database is always personal_memory
- names must match ^[a-z][a-z0-9_]{0,62}$
- system.* and internal service collection names are reserved
- a configurable maximum number of collections is enforced
- creating an existing collection is idempotent
- collection deletion is not exposed in version one
```

The service determines accessibility from the bootstrap list and MongoDB's collection catalog after filtering out reserved names. The dedicated application user is the only non-administrative writer to this database. MongoDB administrative operations remain inaccessible.

`accounts`, `payment_channels`, `transactions`, `events`, `reminders`, and `reminder_completions` are managed collections with strict MongoDB validation and application-level rules. User-created collections remain flexible except for the service-wide safety restrictions. Managed events also provide a bounded `attributes` object for event-specific JSON scalars and scalar arrays; these attributes never affect finance behavior or receive automatic indexes.

### Example transaction

```javascript
{
  _id: ObjectId(...),
  schemaVersion: 1,
  occurredOn: "2026-08-15",
  timezone: "Asia/Bangkok",
  amount: {
    minor: NumberLong(48500),
    currency: "THB"
  },
  transactionKind: "expense",
  accountId: "kbank-visa",
  descriptor: {
    raw: "Fuji",
    normalized: "fuji"
  },
  merchantName: "Fuji",
  categoryPath: ["food"],
  note: null,
  resolution: {
    status: "manual"
  },
  source: {
    type: "direct_entry",
    requestId: "9c3e06d5-34c0-4fc5-88cd-dd68a4db7a64",
    requestHash: "<server-generated SHA-256>",
    rawText: "Lunch at Fuji, 485 baht, paid with KBank Visa."
  },
  createdAt: ISODate(...),
  updatedAt: ISODate(...)
}
```

`occurredOn` is always required. `occurredAt` is optional and is stored as a UTC BSON date only when the source provides an actual time. When `occurredAt` exists, the service verifies that it maps to `occurredOn` in `timezone`.

### Example general memory

```javascript
{
  _id: ObjectId(...),
  type: "preference",
  topic: "travel",
  content: "Prefers hotels near train stations",
  tags: ["travel", "hotel"],
  createdAt: ISODate(...),
  updatedAt: ISODate(...)
}
```

## Recurring Reminder Model

A recurring obligation is stored once as a reminder rule. The service calculates dated occurrences when a morning digest is requested; it does not generate an unbounded set of future documents.

```javascript
{
  _id: "company-uniform",
  schemaVersion: 1,
  title: "Wear the company uniform",
  timezone: "Asia/Bangkok",
  weekdays: ["monday", "friday"],
  startsOn: "2026-08-17",
  preparation: {
    title: "Wash the company uniform",
    leadDays: 2
  },
  active: true,
  createdAt: ISODate(...),
  updatedAt: ISODate(...)
}
```

The preparation step appears from `leadDays` before each occurrence through the day before it, but only while that occurrence's preparation remains incomplete. The obligation itself appears on the occurrence date. Completion is recorded separately by `(reminderId, occurrenceOn, phase)`, where `phase` is `preparation` or `occurrence`; completing one occurrence never completes the recurring rule.

The digest is pull-based. It is calculated only when requested and does not send notifications or run a scheduler.

## API Philosophy

Do **not** build a large conventional REST API with endpoints for every possible question.

Build a **thin MongoDB bridge / memory service**.

Version one exposes REST over HTTPS. The model should be able to request fine-grained operations while MongoDB performs filtering and aggregation. MCP will later adapt these same application operations rather than bypassing them.

The bridge accepts a deliberately constrained subset of MongoDB queries. It is not an arbitrary MongoDB command proxy.

Generic reads and aggregations may access managed and flexible collections. Generic writes may access only flexible collections. Writes to any managed collection must pass through its domain-specific interface so callers cannot bypass semantic validation.

Initial low-level operations:

```text
listCollections
createCollection
find
findOne
insertOne
updateOne
deleteOne
aggregate
```

Possible signatures:

```text
listCollections()

createCollection(name)

find(collection, filter, projection, sort, limit, cursor)

findOne(collection, filter, projection)

insertOne(collection, document)

updateOne(collection, filter, update)

deleteOne(collection, filter)

aggregate(collection, pipeline)
```

## Security Restrictions

Do not expose unrestricted MongoDB administrative access.

The thin service must enforce:

```text
allowed database = personal_memory

allowed collections = bootstrap collections plus collections created through the service

maximum returned records per request
maximum aggregation result size
maximum request body size
maximum pipeline stages
maximum query execution time
maximum number of collections
```

Filters, projections, sorts, updates, and aggregation pipelines must be recursively validated. Version one must use explicit allowlists for accepted MongoDB operators and aggregation stages. At minimum, reject server-side JavaScript and stages that write data or access other collections, including:

```text
$where
$function
$accumulator
$out
$merge
$lookup
$unionWith
```

`updateOne` and `deleteOne` must reject empty filters. Clients may not set or modify `_id`, `createdAt`, or `updatedAt`. The service owns timestamps.

Version-one allowlists:

```text
filter operators:
  $and $or $nor $eq $ne $gt $gte $lt $lte $in $nin $exists

update operators:
  $set $unset $inc

aggregation stages:
  $match $project $sort $limit $skip $group $count $unwind

aggregation expressions:
  $sum $avg $min $max $first $last $dateToString $literal
```

Regular expressions, JavaScript, cross-collection access, and disk-backed aggregation are not supported in version one. Projection values are limited to `0` and `1`; sort values are limited to `1` and `-1`. Reject unknown request fields and any dollar-prefixed key that is not explicitly allowed in its current context.

Do not expose operations such as:

```text
dropDatabase
dropCollection
createUser
server administration commands
arbitrary Mongo shell execution
```

Use a dedicated MongoDB application user with access only to the required database.

Keep MongoDB credentials in Railway environment variables.

Version one uses a single static bearer token. Store only the configured token in an environment variable, compare it in constant time, require it on every `/v1` endpoint, and never include it in logs or error responses.

Version one is strictly single-user. Documents do not contain speculative tenant or user identifiers. Multi-user identity, authorization, sharing, and isolation require a separate design before they are introduced.

If multi-user sharing is added later, treat it as a shared-memory graph rather than ordinary account tenancy: each memory needs an owner, provenance, an explicit sharing scope, and revocable access. Private memories must remain separate from shared memories. This "Khala" direction is a future product concept, not a version-one requirement.

## Higher-Level Tools

Later, add semantic tools on top of the low-level Mongo operations:

```text
remember(...)
recall(...)
forget(...)
searchMemory(...)

recordTransaction(...)
updateTransaction(...)
expenseSummary(...)
```

Example:

```text
recall("What exercise equipment does the user have at home?")
```

The memory service should determine how to retrieve the relevant information rather than forcing the AI client to know every collection/schema detail.

These tools and the future MCP adapter must call the same constrained application interface used by REST. They must not receive direct database access.

## Version-One HTTP Contract

Requests and responses use `application/json`. BSON-specific values use MongoDB Extended JSON so dates, object identifiers, and numeric types survive round trips without ambiguity.

Operation request bodies use a consistent envelope:

```json
{
  "collection": "memories",
  "filter": {},
  "projection": {},
  "sort": {},
  "limit": 50
}
```

Each endpoint accepts only the fields relevant to that operation. `find` defaults to 50 results and cannot exceed the configured maximum. Version one does not provide an opaque cursor; callers can paginate explicitly with an indexed field such as `_id` or `occurredOn`.

Successful reads return:

```json
{
  "data": [],
  "meta": {
    "count": 0
  }
}
```

Successful writes return the inserted identifier or MongoDB-style `matchedCount`, `modifiedCount`, and `deletedCount` values as applicable.

Errors use a stable envelope and do not expose MongoDB internals:

```json
{
  "error": {
    "code": "invalid_filter",
    "message": "filter must contain at least one field"
  }
}
```

The implementation must document status codes and stable error codes beside each endpoint before handler code is considered complete.

## Token-Efficiency Principle

Filtering and aggregation should happen in MongoDB whenever possible.

Bad:

```text
MongoDB → thousands of expense records → ChatGPT → calculate total
```

Preferred:

```text
ChatGPT
   ↓
aggregate(...)
   ↓
MongoDB calculates result
   ↓
small aggregated response
```

Example request:

> Compare food spending by month for the last six months.

MongoDB should return something approximately like:

```json
[
  {"month": "2026-03", "amount": 12350},
  {"month": "2026-04", "amount": 14820},
  {"month": "2026-05", "amount": 11970}
]
```

rather than returning every transaction.

## Expense Input Example

User:

```text
Lunch at Fuji, 485 baht, paid with KBank Visa.
```

The AI should translate that into:

```javascript
{
  occurredOn: "2026-08-15",
  timezone: "Asia/Bangkok",
  amount: {
    minor: 48500,
    currency: "THB"
  },
  transactionKind: "expense",
  accountId: "kbank-visa",
  descriptor: {
    raw: "Fuji",
    normalized: "fuji"
  },
  merchantName: "Fuji",
  categoryPath: ["food"]
}
```

Because the message supplies a date but no actual time, `occurredAt` is omitted. The transaction is stored through the managed finance interface, not generic `insertOne`.

## Future Semantic Search

Design the service so semantic/vector retrieval can be added later without changing the high-level interface.

Future flow:

```text
recall(query)
    │
    ├── structured MongoDB query
    ├── full-text search
    └── vector/semantic search
```

The database remains the source of truth.

## Immediate Implementation Target

Build the smallest working version:

```text
Railway
├── memory-api
└── mongodb
```

The first version should support:

```text
GET /health/live
GET /health/ready

GET  /v1/collections
POST /v1/collections
POST /v1/accounts
POST /v1/transactions
POST /v1/events
GET  /v1/events/{id}
POST /v1/events/search
PATCH /v1/events/{id}
DELETE /v1/events/{id}
POST /v1/reminders
GET  /v1/reminders/digest
POST /v1/reminders/completions
POST /v1/mongo/find
POST /v1/mongo/find-one
POST /v1/mongo/insert-one
POST /v1/mongo/update-one
POST /v1/mongo/delete-one
POST /v1/mongo/aggregate
```

Both health endpoints return only minimal status information and do not require authentication so deployment infrastructure can call them. All `/v1` endpoints require authentication.

Requirements:
- Authentication
- MongoDB connection through environment variable
- Controlled collection registry and collection-name validation
- Strict validation for all managed collections
- Generic write rejection for managed collections
- Result limit
- Input validation
- MongoDB operator and aggregation-stage allowlists
- Request-size, execution-time, and pipeline limits
- Error handling
- Server-owned created/updated timestamps
- Structured JSON errors with stable error codes
- Redaction of credentials and personal data from logs
- Graceful shutdown and separate liveness/readiness behavior
- Docker-compatible deployment
- Local Docker Compose configuration
- Railway configuration

Do not add UI yet.

Do not add vector search yet.

Do not add complex business logic yet.

First milestone:

> Successfully deploy the REST service and MongoDB on Railway; authenticate; create an account and a flexible collection; insert, retrieve, update, and delete a test document; insert a validated transaction through the managed finance interface; and run an aggregation query through the service.

The milestone must also demonstrate that the service rejects:

```text
- missing or incorrect bearer tokens
- invalid and reserved collection names
- access to reserved or system collections
- generic writes to managed collections
- empty update and delete filters
- forbidden MongoDB operators and aggregation stages
- requests and results exceeding configured limits
```

## Portability and Migration

The application receives all deployment-specific values through environment variables:

```text
HTTP_ADDR
MONGODB_URI
MONGODB_DATABASE
AUTH_BEARER_TOKEN
DEFAULT_TIMEZONE
MAX_COLLECTIONS
MAX_REQUEST_BYTES
MAX_RESULT_RECORDS
MAX_RESULT_BYTES
MAX_PIPELINE_STAGES
MONGODB_OPERATION_TIMEOUT
```

Version-one defaults:

```text
MAX_COLLECTIONS=100
MAX_REQUEST_BYTES=1048576
MAX_RESULT_RECORDS=100
MAX_RESULT_BYTES=1048576
MAX_PIPELINE_STAGES=10
MONGODB_OPERATION_TIMEOUT=5s
```

Do not call Railway-specific interfaces from application code. Railway configuration may inject variables and networking details, but it must not change the application's behavior or HTTP contract.

The repository must include a local Docker Compose setup using the same application image and a MongoDB service with a persistent volume. Moving to a self-hosted machine should consist of transferring database data or restoring a backup, supplying equivalent environment variables, providing HTTPS ingress, and starting the same containers.

Backups, restore verification, and public ingress for the self-hosted machine must be designed before migration; they are not part of the first Railway milestone.

## Implementation Phases

1. Lock the version-one HTTP contract and MongoDB safety policy.
2. Lock expense representation, validation, timestamps, and timezone behavior.
3. Implement the Go application and MongoDB adapter with automated tests.
4. Verify the complete milestone locally through Docker Compose.
5. Deploy to Railway and run the same acceptance tests.
6. Document and rehearse backup, restore, and self-hosted migration.

## Suggested Repository Structure

```text
personal-memory/
├── cmd/
│   └── server/
├── internal/
│   ├── config/
│   ├── database/
│   ├── handlers/
│   ├── middleware/
│   └── models/
├── Dockerfile
├── go.mod
├── go.sum
└── README.md
```

Preferred implementation language: **Go**, unless there is a strong reason to use another stack.

## Codex Task

Implement the first milestone only.

Prioritize:
1. Simple architecture
2. Safe Mongo access
3. Small API surface
4. Deployment portability
5. Railway deployment
6. Easy future exposure as MCP tools

Avoid unnecessary abstractions and premature vector-search or AI-memory logic.
