# Expense Memo Ingestion and Transaction Validation

## Purpose

Define a strict but lightweight contract for turning pasted personal-finance memos into validated MongoDB documents.

This document covers:

- the copy-and-paste grammar;
- parsing and review behavior;
- the API request schema;
- the stored transaction schema;
- alias application;
- MongoDB validation and indexes;
- instructions for Codex when handling an import.

## Important collection decision

Use a `transactions` collection, not an `expenses` collection.

The input already contains more than expenses:

- `+18000` is income;
- `-14054.02 krungsri payment` is a credit-card payment;
- `-218 7-eleven` may be a refund;
- ordinary unsigned entries are usually expenses.

Putting all of these in `expenses` makes aggregation unsafe because a query can accidentally count income or card payments as spending. If the collection must remain named `expenses`, only `expense` and `refund` transaction kinds should be accepted there.

## Design principles

1. Preserve the original memo and every raw line.
2. Parse first and commit second.
3. Never silently guess uncertain merchants, categories, refunds, or payment paths.
4. Use integer minor units, such as satang, instead of floating-point money.
5. Store calendar-only dates as `YYYY-MM-DD`; do not invent a time of day.
6. Keep aliases separate from transactions and record which alias version was applied.
7. Treat duplicate-looking lines as valid. Real transactions can have identical date, amount, and descriptor.
8. Make retries idempotent with `(importId, lineNumber)`, not with transaction content.

## Copy-and-paste format: `expense-memo-v1`

The account, currency, and timezone belong to the import request, not to every line.

```text
2026-07-11
165 7-eleven
13 7-eleven
25 bts
493.90 paragon kinokuniya
65 bts

2026-07-10
65 bts
50 ทางด่วน
36 7-eleven
```

### Line grammar

An input line is one of:

1. blank line;
2. date heading;
3. transaction line.

Date heading:

```regex
^\d{4}-\d{2}-\d{2}$
```

Transaction line:

```regex
^(?<sign>[+-]?)(?<amount>(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d{1,2})?)(?:\s+(?<descriptor>.*\S))?$
```

### Parsing rules

- A transaction line must follow a valid date heading.
- Commas in an amount are accepted and removed during normalization.
- At most two decimal places are accepted.
- Zero and negative magnitudes are rejected after the sign is separated.
- Leading `+` means `income`.
- Leading `-` means a negative account-side event, but does **not** by itself distinguish `refund` from `credit_card_payment`.
- An unsigned amount defaults to `expense`.
- A missing descriptor is allowed only for explicitly signed income, for example `+18000`; it remains unresolved.
- Preserve Thai, emoji, punctuation, capitalization, and question marks in the raw descriptor.
- Normalize only for matching: Unicode NFKC, trim, collapse whitespace, and lowercase.

### Negative-line rule

Do not classify every negative credit-card line as a payment.

```text
-61975.37 payment   -> credit_card_payment
-218 7-eleven       -> refund candidate or needs_review
```

A negative line becomes `credit_card_payment` only when an exact alias or an explicit descriptor rule identifies it as a payment. Otherwise return `needs_review`; do not guess from the sign alone.

## Validation pipeline

```text
raw import request
    -> envelope validation
    -> line parsing
    -> transaction candidates
    -> exact alias resolution
    -> semantic validation
    -> dry-run preview
    -> user confirmation
    -> atomic insert
```

### Stage 1: envelope validation

Validate before parsing:

- `importId` is a UUID supplied by the client and reused for retries;
- `accountId` exists and is active;
- `currency` is an uppercase ISO-4217 code;
- `timezone` is a valid IANA timezone;
- `memo` is non-empty and below the configured size limit;
- `dryRun` defaults to `true`.

### Stage 2: candidate validation

Return one result per nonblank input line:

```json
{
  "lineNumber": 2,
  "rawLine": "165 7-eleven",
  "status": "valid",
  "occurredOn": "2026-07-11",
  "amountMinor": 16500,
  "inputSign": "none",
  "descriptorRaw": "7-eleven",
  "descriptorNormalized": "7-eleven",
  "transactionKind": "expense",
  "resolution": "alias",
  "aliasKey": "7-eleven",
  "issues": []
}
```

Candidate status is one of:

- `valid`: safe to commit;
- `needs_review`: parsed, but its meaning is uncertain;
- `rejected`: syntactically or semantically invalid.

Do not insert `needs_review` or `rejected` candidates unless the user explicitly confirms a correction.

## Import request JSON Schema

Use this at the HTTP or MCP boundary. MongoDB validation is still required separately.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.invalid/schemas/expense-memo-import-v1.json",
  "title": "ExpenseMemoImportRequestV1",
  "type": "object",
  "additionalProperties": false,
  "required": ["importId", "accountId", "memo"],
  "properties": {
    "importId": {
      "type": "string",
      "format": "uuid"
    },
    "accountId": {
      "type": "string",
      "pattern": "^[a-z0-9]+(?:-[a-z0-9]+)*$",
      "minLength": 1,
      "maxLength": 100
    },
    "memo": {
      "type": "string",
      "minLength": 1,
      "maxLength": 50000
    },
    "currency": {
      "type": "string",
      "pattern": "^[A-Z]{3}$",
      "default": "THB"
    },
    "timezone": {
      "type": "string",
      "default": "Asia/Bangkok"
    },
    "dryRun": {
      "type": "boolean",
      "default": true
    }
  }
}
```

JSON Schema cannot verify that an IANA timezone or `accountId` exists. Those checks belong in application validation.

## Stored transaction document

Store money as a positive magnitude. `transactionKind` supplies its meaning.

```javascript
{
  _id: ObjectId("..."),
  schemaVersion: 1,

  occurredOn: "2026-07-11",
  // occurredAt is omitted because this memo supplied no actual time.
  timezone: "Asia/Bangkok",

  amount: {
    minor: NumberLong(16500),
    currency: "THB"
  },

  transactionKind: "expense",
  accountId: "krungsri-homepro-credit-card",
  paymentChannelId: "truemoney-wallet",

  descriptor: {
    raw: "7-eleven",
    normalized: "7-eleven"
  },

  merchantName: "7-Eleven",
  categoryPath: ["eat", "7-eleven"],
  note: null,

  resolution: {
    status: "alias",
    aliasKey: "7-eleven",
    aliasVersion: 3
  },

  source: {
    type: "expense_memo",
    importId: "8b2a9e7b-4af2-41ca-ae31-9688a96d3925",
    lineNumber: 2,
    rawLine: "165 7-eleven",
    parserVersion: "expense-memo-v1"
  },

  createdAt: ISODate("2026-08-15T00:00:00Z"),
  updatedAt: ISODate("2026-08-15T00:00:00Z")
}
```

### Transaction kinds

Use this closed set for version 1:

```text
expense
income
refund
credit_card_payment
transfer
```

Do not use `charge` and `outflow` as economic categories. They describe account mechanics, while both may still be an `expense`.

### Calendar dates

`occurredOn` is a required date-only string. It represents the calendar date on which the transaction occurred in `timezone`.

`occurredAt` is an optional UTC BSON date. Store it only when the source supplies an actual time; never invent midnight for a date-only memo. When it is present, also store `occurredOn` and require it to equal the local calendar date obtained by interpreting `occurredAt` in `timezone`.

Keeping the fields separate preserves the difference between a known calendar date and a known instant. It also avoids mixing strings and BSON dates in one indexed field.

### Direct-entry source

Transactions created directly from a conversational or structured client share the same collection but use a different source shape:

```javascript
source: {
  type: "direct_entry",
  requestId: "9c3e06d5-34c0-4fc5-88cd-dd68a4db7a64",
  requestHash: "server-generated SHA-256 of validated request content after defaults",
  rawText: "Lunch at Fuji, 485 baht, paid with KBank Visa."
}
```

The client supplies the UUID `requestId` and reuses it only to retry the same logical transaction. The service generates `requestHash`. Reusing a request ID with identical content returns the existing transaction; reusing it with different content returns an idempotency conflict. `rawText` is optional.

### Money

`amount.minor` is an integer in the currency's minor unit:

```text
165 THB    -> 16500
493.90 THB -> 49390
```

Do not store money as BSON `double`.

### Categories

Store category segments as an array:

```javascript
["eat"]
["eat", "7-eleven"]
["transport", "grab", "bike"]
```

Join the array with `.` for display. Do not store placeholder segments such as `.` and do not create fixed `category1`, `category2`, and `category3` fields.

## Alias model

Aliases should be stored separately from transactions.

```javascript
{
  aliasKey: "7-eleven",
  version: 3,
  enabled: true,
  match: {
    descriptorNormalized: "7-eleven"
  },
  defaults: {
    merchantName: "7-Eleven",
    categoryPath: ["eat", "7-eleven"]
  },
  scopedOverrides: [
    {
      accountId: "krungsri-credit-card",
      paymentChannelId: "truemoney-wallet"
    }
  ],
  createdAt: ISODate("..."),
  updatedAt: ISODate("...")
}
```

This separation matters because `7-eleven` may be paid through TrueMoney on one card and directly on another card. A global alias may set merchant and category, but an account-scoped override may set a payment channel.

Alias precedence:

1. values explicitly supplied for the transaction;
2. account-scoped alias override;
3. global alias defaults;
4. unresolved.

Use exact normalized matching automatically. Fuzzy matching may suggest an alias, but must not apply it without confirmation.

## MongoDB validator

The database validator catches malformed persisted documents. Cross-field rules remain in the application.

```javascript
db.runCommand({
  collMod: "transactions",
  validator: {
    $jsonSchema: {
      bsonType: "object",
      additionalProperties: false,
      required: [
        "_id",
        "schemaVersion",
        "occurredOn",
        "timezone",
        "amount",
        "transactionKind",
        "accountId",
        "descriptor",
        "resolution",
        "source",
        "createdAt",
        "updatedAt"
      ],
      properties: {
        _id: { bsonType: "objectId" },
        schemaVersion: { bsonType: ["int", "long"], minimum: 1 },
        occurredOn: {
          bsonType: "string",
          pattern: "^\\d{4}-\\d{2}-\\d{2}$"
        },
        occurredAt: { bsonType: "date" },
        timezone: { bsonType: "string", minLength: 1, maxLength: 100 },
        amount: {
          bsonType: "object",
          additionalProperties: false,
          required: ["minor", "currency"],
          properties: {
            minor: { bsonType: ["int", "long"], minimum: 1 },
            currency: { bsonType: "string", pattern: "^[A-Z]{3}$" }
          }
        },
        transactionKind: {
          enum: [
            "expense",
            "income",
            "refund",
            "credit_card_payment",
            "transfer"
          ]
        },
        accountId: { bsonType: "string", minLength: 1, maxLength: 100 },
        paymentChannelId: {
          bsonType: ["string", "null"],
          maxLength: 100
        },
        descriptor: {
          bsonType: "object",
          additionalProperties: false,
          required: ["raw", "normalized"],
          properties: {
            raw: { bsonType: ["string", "null"], maxLength: 1000 },
            normalized: { bsonType: ["string", "null"], maxLength: 1000 }
          }
        },
        merchantName: { bsonType: ["string", "null"], maxLength: 300 },
        categoryPath: {
          bsonType: ["array", "null"],
          maxItems: 8,
          items: { bsonType: "string", minLength: 1, maxLength: 100 }
        },
        note: { bsonType: ["string", "null"], maxLength: 5000 },
        resolution: {
          bsonType: "object",
          additionalProperties: false,
          required: ["status"],
          properties: {
            status: { enum: ["unresolved", "alias", "manual"] },
            aliasKey: { bsonType: "string", maxLength: 200 },
            aliasVersion: { bsonType: ["int", "long"], minimum: 1 }
          }
        },
        source: {
          bsonType: "object",
          additionalProperties: false,
          required: ["type"],
          properties: {
            type: { enum: ["direct_entry", "expense_memo"] },
            requestId: { bsonType: "string" },
            requestHash: { bsonType: "string", pattern: "^[a-f0-9]{64}$" },
            rawText: { bsonType: ["string", "null"], maxLength: 5000 },
            importId: { bsonType: "string" },
            lineNumber: { bsonType: ["int", "long"], minimum: 1 },
            rawLine: { bsonType: "string", maxLength: 2000 },
            parserVersion: { bsonType: "string", maxLength: 100 }
          }
        },
        createdAt: { bsonType: "date" },
        updatedAt: { bsonType: "date" }
      }
    }
  },
  validationLevel: "strict",
  validationAction: "error"
});
```

For a new collection, put the same validator in `db.createCollection("transactions", { validator: ... })` instead of using `collMod`.

## Application-level semantic rules

MongoDB's validator is not sufficient. Enforce these rules before insertion:

1. `occurredOn` must be a real calendar date.
2. `timezone` must resolve through an IANA timezone database.
3. If `occurredAt` exists, its local date in `timezone` must equal `occurredOn`.
4. A `direct_entry` source requires `requestId` and `requestHash` and forbids expense-memo fields.
5. An `expense_memo` source requires `importId`, `lineNumber`, `rawLine`, and `parserVersion` and forbids direct-entry fields.
6. `accountId` and `paymentChannelId` must reference configured accounts/channels.
7. `amount.minor` must fit the application's safe integer range and configured maximum.
8. `income` requires an explicit `+` or manual confirmation.
9. A negative line without an exact payment/refund rule is `needs_review`.
10. `resolution.status = alias` requires `aliasKey` and `aliasVersion`.
11. `categoryPath` must contain normalized segments and no literal `.` segment.
12. An alias must never overwrite an explicitly supplied account or payment channel.
13. A commit must fail as a unit if any selected candidate fails validation.

## Indexes

```javascript
db.transactions.createIndex(
  { "source.requestId": 1 },
  {
    unique: true,
    name: "uniq_direct_request",
    partialFilterExpression: { "source.type": "direct_entry" }
  }
);

db.transactions.createIndex(
  { "source.importId": 1, "source.lineNumber": 1 },
  {
    unique: true,
    name: "uniq_import_line",
    partialFilterExpression: { "source.type": "expense_memo" }
  }
);

db.transactions.createIndex(
  { accountId: 1, occurredOn: -1 },
  { name: "account_date" }
);

db.transactions.createIndex(
  { transactionKind: 1, occurredOn: -1 },
  { name: "kind_date" }
);

db.transactions.createIndex(
  { categoryPath: 1, occurredOn: -1 },
  { name: "category_date" }
);
```

Do not create a unique index from date, amount, and descriptor. These are legitimate duplicates:

```text
2026-07-11
29 burgerking
29 burgerking
```

## Dry-run response

Before committing, return a compact preview:

```json
{
  "importId": "8b2a9e7b-4af2-41ca-ae31-9688a96d3925",
  "summary": {
    "valid": 4,
    "needsReview": 1,
    "rejected": 0
  },
  "metadataStrips": [
    { "value": "7-eleven", "count": 2, "resolution": "alias" },
    { "value": "bts", "count": 2, "resolution": "alias" },
    { "value": "paragon kinokuniya", "count": 1, "resolution": "unresolved" }
  ],
  "candidates": []
}
```

Return all candidates only when requested. Default responses should remain compact for token efficiency.

## Alias-promotion workflow

When an unresolved descriptor appears:

1. show the exact raw descriptor and its occurrence count;
2. offer `leave unresolved`, `resolve once`, or `promote to alias`;
3. if promoted, collect merchant, category path, and optional scoped payment channel;
4. preview affected candidates;
5. save the alias only after confirmation;
6. record the alias version on every resolved transaction.

Do not retroactively rewrite old transactions when an alias changes unless the user requests a migration. Transactions should retain their resolved snapshot for auditability.

## Codex operating instructions

The following block can be copied into a project instruction file:

```markdown
When the user supplies an expense memo:

1. Treat the submission as `expense-memo-v1` and preserve the complete raw text.
2. Require an account context; default currency to THB and timezone to Asia/Bangkok.
3. Perform a dry run before any insert.
4. Parse date headings and amount-description lines without inventing timestamps.
5. Store money as integer minor units, never floating point.
6. Apply only exact, enabled aliases automatically.
7. Show all distinct descriptors with counts and resolution status.
8. Do not infer that every negative credit-card amount is a payment.
9. Mark uncertain negative lines and fuzzy alias matches as `needs_review`.
10. Preserve duplicate-looking transactions within one memo.
11. Commit only confirmed `valid` candidates, using `(importId, lineNumber)` for idempotency.
12. Return aggregate summaries by default; return raw transactions only when requested.
```

## Minimum acceptance tests

1. A transaction before any date heading is rejected.
2. An invalid calendar date such as `2026-02-30` is rejected.
3. `493.9` normalizes to `49390` minor units for THB.
4. More than two decimal places is rejected for THB.
5. `+18000` becomes income with a null descriptor.
6. `-14054.02 payment` becomes a credit-card payment only through an explicit rule.
7. `-218 7-eleven` requires refund/payment review unless an exact rule exists.
8. Thai descriptors survive a parse/serialize round trip unchanged.
9. Identical transaction lines remain distinct when their line numbers differ.
10. Retrying the same `importId` does not create duplicates.
11. `eat.7-eleven` is stored as `["eat", "7-eleven"]`.
12. A global alias does not overwrite an explicitly supplied account.

## Recommended first implementation slice

Implement only:

1. `POST /expense-imports/preview`;
2. `POST /expense-imports/{importId}/commit`;
3. the `transactions` validator and indexes;
4. exact alias lookup;
5. dry-run diagnostics.

Keep low-level Mongo writes to managed finance collections private to the service. The constrained bridge may still serve flexible collections and may read or aggregate managed collections, but it must reject generic writes to `transactions`. Exposing `insertOne` for this collection would allow an AI client to bypass application-level domain validation.
