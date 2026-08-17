# Personal Memory

This context stores personal facts, financial events, and reminders while preserving enough source information for review and correction.

## Language

### Finance

**Expense memo**:
A pasted block of date headings and amount-description lines submitted for parsing.
_Avoid_: Expense list, statement

**Import batch**:
One submission of an expense memo together with its account, currency, and timezone context.
_Avoid_: Upload, paste

**Transaction candidate**:
A parsed memo line that has not yet passed validation and confirmation.
_Avoid_: Temporary expense, draft row

**Transaction**:
A confirmed financial event, including expenses, income, refunds, card payments, and internal transfers.
_Avoid_: Expense, record

**Transaction kind**:
The economic meaning of a transaction: expense, income, refund, credit-card payment, or transfer.
_Avoid_: Flow type, direction

**Descriptor**:
The free text following the amount in a memo line, preserved exactly as entered.
_Avoid_: Metadata strip, category

**Alias**:
A user-approved rule that maps a normalized descriptor to reusable transaction fields.
_Avoid_: Guess, automatic category

**Resolution**:
How transaction fields were established: unresolved, alias, or manual.
_Avoid_: Parsing status

**Account**:
The bank account, credit card, cash wallet, or other balance against which the transaction was observed.
_Avoid_: Payment method, source

**Payment channel**:
An optional intermediary used for payment, such as TrueMoney Wallet or LINE Pay.
_Avoid_: Account, payment source

**Category path**:
An ordered hierarchy of category segments, displayed by joining segments with a dot.
_Avoid_: Category 1, Category 2, Category 3

### Reminders

**Reminder rule**:
A continuing obligation together with the calendar pattern on which it recurs.
_Avoid_: Reminder row, recurring event

**Reminder occurrence**:
One dated instance of a reminder rule.
_Avoid_: Event, generated reminder

**Preparation step**:
An action that must be completed before a reminder occurrence and is repeated in the digest while incomplete.
_Avoid_: Early reminder, pre-event

**Completion**:
An acknowledgement that one step of one reminder occurrence is done; it does not complete the reminder rule.
_Avoid_: Dismissal, rule completion

**Morning digest**:
The reminders due on a requested local calendar date, excluding completed steps.
_Avoid_: Notification, scheduled message
