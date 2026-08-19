# Feature Request: Add Managed Event / Life Log Support

## Status

Proposed for version one.

## Summary

Promote the existing `events` bootstrap collection to a managed collection and add focused operations for recording, searching, correcting, and deleting completed personal or work-related events.

An event may contain informational monetary context, but an event is never itself an expense, income item, transfer, refund, or account-balance adjustment.

## Existing System Context

Anna already creates an `events` collection during database bootstrap. It is currently flexible: it has no collection validator, indexes, application-level validation, or focused HTTP operations.

This feature does not create another collection. It makes the existing `events` collection managed, following the same rule used by other managed collections: generic reads remain allowed, but all writes must pass through the event interface.

## Problem

Some memorable activities include monetary details without representing personal spending.

Example:

**19 Aug 2026 — Company monthly dinner**

* Place: Sushiro Theprak branch
* Food bill: ฿410
* Service charge: 10%
* Total value: ฿451
* Company allowance: ฿500 per person
* Personal payment: ฿0

The company covered the entire ฿451. Recording ฿451 as an expense would incorrectly increase the user's personal spending. Discarding the message would lose useful personal context.

The correct result is one event with explicitly confirmed zero personal payment and no financial transaction.

## Goals

* Store completed, dated events without creating financial transactions.
* Preserve the user's original Thai, English, or mixed-language text.
* Store optional monetary context without affecting financial calculations.
* Make events searchable by occurrence date, creation time, type, title, location, people, and tags.
* Prevent duplicate events when a client retries a request.
* Link an event to related personal transactions when both are recorded.
* Allow a user to correct or delete an event through validated operations.
* Allow bounded event-specific fields without weakening the managed core schema.

## Non-Goals

The first version does not include:

* future appointments, start/end intervals, or event attendance states;
* recurring events;
* reminder or morning-digest integration;
* semantic, fuzzy, or substring search;
* automatic creation of an event for every financial transaction;
* automatic reimbursement or allowance accounting;
* financial context containing more than one currency;
* cross-document database transactions between events and financial transactions.

Future appointments belong in a separate scheduling design. This feature records things that have already occurred, including an event recorded later than its occurrence date.

## Terms

* **Event** — a completed occurrence worth retaining as personal context.
* **Personal transaction** — money that moved into or out of one of the user's accounts.
* **Financial context** — informational amounts associated with an event. These values never participate in financial totals.
* **Related transaction** — a separately stored personal transaction connected to an event by identifier.

## Event Model

The managed `events` collection stores the following shape:

```javascript
{
  _id: ObjectId,
  schemaVersion: 1,
  occurredOn: "YYYY-MM-DD",
  occurredAt: Date, // absent when the source supplies no actual time
  timezone: "IANA timezone",
  title: "string",
  eventType: "event-type",
  location: "string" | null,
  locationNormalized: "server-generated-string" | null,
  people: ["string"],
  peopleNormalized: ["server-generated-string"],
  description: "string" | null,
  tags: ["normalized-tag"],
  financialContext: {
    currency: "ISO-4217",
    totalValueMinor: NumberLong | null,
    allowanceMinor: NumberLong | null,
    coveredByOthersMinor: NumberLong | null,
    personalPaymentMinor: NumberLong | null
  } | null,
  relatedTransactionIds: [ObjectId],
  attributes: {
    eventSpecificKey: "JSON scalar or scalar array"
  } | null,
  source: {
    type: "direct_entry",
    requestId: "UUID",
    requestHash: "SHA-256 hex digest",
    rawText: "string"
  },
  searchTokens: ["server-generated-token"],
  createdAt: Date,
  updatedAt: Date
}
```

`_id`, `schemaVersion`, `source.type`, `source.requestHash`, `locationNormalized`, `peopleNormalized`, `searchTokens`, `createdAt`, and `updatedAt` are server-owned.

### Required Input

* `requestId`
* `occurredOn`
* `title`
* `rawText`

### Defaults

* Omitted `timezone` uses `DEFAULT_TIMEZONE` and is stored explicitly.
* Omitted `eventType` becomes `general-memory`.
* Omitted array fields become empty arrays.
* Omitted optional scalar and object fields are stored as `null`, except `occurredAt`, which is absent when the source supplies no actual time.

### Validation

* `requestId` must be a UUID.
* `occurredOn` must be a real local date in `YYYY-MM-DD` format.
* `occurredAt`, when present, must be an RFC 3339 instant whose local date in `timezone` equals `occurredOn`.
* `timezone` must be a valid IANA timezone.
* `title` must contain 1–300 Unicode characters after trimming.
* `location` must contain at most 500 Unicode characters after trimming.
* `description` and `rawText` must each contain at most 5,000 Unicode characters.
* `people` may contain at most 50 non-empty strings of at most 200 Unicode characters each.
* `tags` may contain at most 20 non-empty strings of at most 50 Unicode characters each.
* Tags are trimmed, Unicode-normalized, lowercased, deduplicated, and stored in first-seen order.
* `relatedTransactionIds` may contain at most 20 unique, valid transaction object identifiers.
* `attributes` may contain at most 32 keys and 16 KiB of encoded JSON.
* Attribute keys must contain 1–64 characters, cannot start with `$`, and cannot contain `.`.
* Attribute values must be JSON scalars or arrays of at most 20 scalars. Nested objects and nested arrays are rejected.
* Attribute strings may contain at most 1,000 Unicode characters.
* Attributes cannot shadow core fields, affect financial calculations, or receive automatic indexes.
* Every monetary value must be an integer greater than or equal to zero.
* `financialContext.currency` is required when `financialContext` is present and must be a supported three-letter ISO 4217 code.
* `personalPaymentMinor: 0` is valid only when the source explicitly establishes that the user paid nothing. Unknown payment is represented by omitting the field, not by zero.
* Unknown request fields are rejected.

The event interface validates that each `relatedTransactionId` refers to an existing transaction. It does not require the transaction and event currencies to match because a relationship may be contextual rather than a duplicated monetary representation.

## Event Types

`eventType` must be one of:

```text
company-event
social-event
meal-gathering
appointment-completed
travel
celebration
personal-milestone
work-milestone
health-fitness
pet
family
general-memory
```

New event types require a schema-versioned application change. Callers should use `general-memory` plus tags rather than inventing an unrecognized value.

## Financial Context Rules

Financial context is descriptive only. No field under `financialContext` may be read by expense summaries, income summaries, budgets, account balances, or financial reports.

Meanings:

* `totalValueMinor` — total value attributable to the user for the event.
* `allowanceMinor` — maximum allowance offered for the user, whether or not all of it was consumed.
* `coveredByOthersMinor` — amount actually paid on the user's behalf.
* `personalPaymentMinor` — amount actually paid from the user's own funds.

The fields do not have to add up. For example, an allowance can exceed the total value. The raw text and description preserve details such as service-charge calculations.

When `personalPaymentMinor` is greater than zero, the amount is still informational. The actual personal expense must be recorded separately in `transactions` and linked through `relatedTransactionIds`.

## Assistant Classification Rules

The assistant applies the following decision table:

| User meaning | Event | Transaction |
|---|---:|---:|
| Explicitly remember a completed occurrence; no personal money moved | Create | Do not create |
| Record personal spending only; no request to retain broader event context | Do not create | Create |
| Explicitly remember an occurrence and personal money moved | Create | Create and link |
| Payment responsibility or personal amount is unclear | Ask one clarifying question | Create nothing yet |
| Future appointment or planned activity | Do not create | Do not create |

The assistant must not infer that the user personally paid a restaurant bill merely because the message contains a bill total. It must identify who paid and how much left one of the user's accounts.

For a combined event and transaction, the assistant records the transaction first and then records the event with the returned transaction identifier. If transaction creation succeeds but event creation fails, the valid transaction remains stored; the assistant reports the partial result and retries event creation with the same event `requestId`.

## Managed Event Interface

### Create

```text
POST /v1/events
operationId: recordEvent
```

Request:

```json
{
  "requestId": "d19f46f2-f760-4764-89ad-a22ae819ce6e",
  "occurredOn": "2026-08-19",
  "timezone": "Asia/Bangkok",
  "title": "Company monthly dinner at Sushiro",
  "eventType": "company-event",
  "location": "Sushiro Theprak branch",
  "people": [],
  "description": "Monthly company dinner. The company covered the full amount.",
  "tags": ["company", "dinner", "sushiro"],
  "financialContext": {
    "currency": "THB",
    "totalValueMinor": 45100,
    "allowanceMinor": 50000,
    "coveredByOthersMinor": 45100,
    "personalPaymentMinor": 0
  },
  "relatedTransactionIds": [],
  "attributes": {
    "restaurantChain": "Sushiro"
  },
  "rawText": "today มีกินเลี้ยงประจำเดือนบริษัท we go to Sushiro Theprak branch. My bill total is 410 baht, with 10% service charge so it is 451 baht. บริษัท give each person 500 baht, so I don't have to pay for it."
}
```

Responses:

* `201` — event created.
* `200` — an identical request with this `requestId` already exists; return its identifier with `created: false`.
* `409 idempotency_conflict` — this `requestId` was previously used for different event content.
* `400 invalid_request` — validation failed.

### Get One

```text
GET /v1/events/{id}
operationId: getEvent
```

Returns `200` with the event or `404 not_found`.

### Search

```text
POST /v1/events/search
operationId: searchEvents
```

Request fields:

```javascript
{
  occurredFrom: "YYYY-MM-DD"?,  // inclusive
  occurredTo: "YYYY-MM-DD"?,    // inclusive
  createdFrom: "RFC3339"?,
  createdTo: "RFC3339"?,        // exclusive
  eventTypes: ["event-type"]?,
  location: "exact normalized location"?,
  people: ["exact normalized person"]?,
  tags: ["normalized-tag"]?,     // all requested tags must match
  text: "token query"?,
  sort: "occurred_desc" | "occurred_asc" | "created_desc"?,
  limit: 1..100?
}
```

Defaults are `sort: occurred_desc` and `limit: 50`. At least one filter must be supplied. Date ranges must be valid and `occurredFrom` must not follow `occurredTo`.

`text` uses server-generated Unicode-normalized, case-folded tokens from `title`, `location`, `people`, and `tags`. All query tokens must match. Version one does not support fuzzy, semantic, or substring matching. Thai phrases without word-separating spaces are matched as whole normalized tokens.

Relative expressions such as “last Friday” and “this month” are resolved by the assistant into absolute date ranges in the user's timezone before calling `searchEvents`.

### Update

```text
PATCH /v1/events/{id}
operationId: updateEvent
```

The request contains one or more mutable event fields and uses JSON Merge Patch null semantics for optional fields. `requestId`, `_id`, `schemaVersion`, `source`, `locationNormalized`, `peopleNormalized`, `searchTokens`, `createdAt`, and `updatedAt` cannot be supplied. The module validates the complete resulting event, regenerates normalized fields and `searchTokens`, and updates `updatedAt` atomically.

Returns `200`, `404 not_found`, or `400 invalid_request`.

### Delete

```text
DELETE /v1/events/{id}
operationId: deleteEvent
```

Deletion removes only the event. It never deletes related financial transactions. Returns `204` when the event existed and was deleted, or `404 not_found` otherwise.

## Storage and Index Requirements

* Add `events` to `IsManagedCollection` so generic event writes are rejected.
* Add a strict MongoDB validator for schema version one.
* Apply or update the validator on the already-existing bootstrap collection; creating the collection only when absent is insufficient.
* Add a unique partial index on `source.requestId` for `source.type = direct_entry`.
* Add indexes supporting:
  * `occurredOn` descending;
  * `eventType` plus `occurredOn` descending;
  * `tags` plus `occurredOn` descending;
  * `locationNormalized` plus `occurredOn` descending;
  * `searchTokens` plus `occurredOn` descending;
  * `createdAt` descending.
* Existing event documents must be validated before strict validation is enabled. Deployment must fail with an actionable migration error rather than silently deleting or rewriting incompatible documents.

## Retrieval Examples

| User question | Search request |
|---|---|
| “What did I do last Friday?” | Exact Friday in `occurredFrom` and `occurredTo` |
| “Show my company events this month.” | Month range plus `eventTypes: ["company-event"]` |
| “Where did we have the company dinner?” | `text: "company dinner"`, newest first |
| “When did I last go to Sushiro?” | `text: "sushiro"`, `sort: "occurred_desc"`, `limit: 1` |
| “List memorable events from August.” | Exact August date range |
| “What events did I record today?” | Today's local-day boundaries converted to UTC instants in `createdFrom` and `createdTo` |

## Acceptance Criteria

### Event-only example

Given the Sushiro message above, when the user asks Anna to remember the dinner:

* exactly one event is stored;
* `occurredOn` is `2026-08-19`;
* `eventType` is `company-event`;
* `financialContext.totalValueMinor` is `45100`;
* `financialContext.allowanceMinor` is `50000`;
* `financialContext.coveredByOthersMinor` is `45100`;
* `financialContext.personalPaymentMinor` is `0`;
* the mixed-language input is preserved exactly in `source.rawText`;
* no transaction is created; and
* personal expense totals do not increase.

### Event plus transaction example

Given a company dinner worth ฿620 where the company pays ฿500 and the user explicitly says they paid ฿120 from their KBank account:

* one ฿120 expense transaction is created against the KBank account;
* one event is created with `totalValueMinor: 62000`, `coveredByOthersMinor: 50000`, and `personalPaymentMinor: 12000`;
* the event contains the transaction identifier in `relatedTransactionIds`; and
* financial totals increase by ฿120, not ฿620.

### Idempotency

* Repeating an identical create request with the same `requestId` returns the original event and does not create a duplicate.
* Reusing the same `requestId` with different content returns `409 idempotency_conflict`.

### Validation and isolation

* An invalid date, timezone, currency, event type, related transaction identifier, or monetary value is rejected.
* `occurredAt` inconsistent with `occurredOn` and `timezone` is rejected.
* Generic insert, update, and delete operations against `events` are rejected as managed-collection writes.
* Event monetary fields are absent from every financial aggregation path.

### Retrieval and correction

* Each retrieval example above returns the expected event from a fixture containing distractor events.
* Thai, English, and mixed-language values round-trip without data loss.
* Guarded `attributes` round-trip without becoming part of core financial behavior or automatic indexes.
* Updating searchable fields changes subsequent search results.
* Deleting an event does not delete or alter any linked transaction.

## Required Deliverables

* Managed event input, output, and search types.
* Application validation and event operations.
* Repository event operations.
* MongoDB validator, validator migration, and indexes.
* HTTP handlers and routes.
* OpenAPI action definitions for all five event operations.
* Unit tests for validation, normalization, classification fixtures, and idempotency.
* HTTP tests for status codes and strict JSON decoding.
* MongoDB integration tests for validators, indexes, searches, and managed-write rejection.
* README examples for recording and searching events.
