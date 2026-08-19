package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type repositoryStub struct {
	collections          []string
	accountActive        bool
	paymentChannelActive bool
	paymentChannel       bson.D
	transaction          bson.D
	transactionHash      string
	event                bson.D
	eventHash            string
	events               []bson.M
	eventSearch          EventSearchInput
	eventDeleted         bool
	deletedEventID       bson.ObjectID
	eventUpdate          bson.D
	reminder             bson.D
	reminderRules        []ReminderRule
	completed            map[ReminderCompletionKey]bool
	completion           bson.D
}

func (stub *repositoryStub) Ping(context.Context) error            { return nil }
func (stub *repositoryStub) EnsureBootstrap(context.Context) error { return nil }
func (stub *repositoryStub) ListCollections(context.Context) ([]string, error) {
	return stub.collections, nil
}
func (stub *repositoryStub) CreateCollection(context.Context, string) error    { return nil }
func (stub *repositoryStub) Find(context.Context, FindInput) ([]bson.M, error) { return nil, nil }
func (stub *repositoryStub) FindOne(context.Context, FindInput) (bson.M, error) {
	return nil, ErrNotFound
}
func (stub *repositoryStub) InsertOne(context.Context, string, bson.D) (any, error) { return "id", nil }
func (stub *repositoryStub) UpdateOne(context.Context, UpdateInput) (WriteResult, error) {
	return WriteResult{}, nil
}
func (stub *repositoryStub) DeleteOne(context.Context, DeleteInput) (WriteResult, error) {
	return WriteResult{}, nil
}
func (stub *repositoryStub) Aggregate(context.Context, AggregateInput) ([]bson.M, error) {
	return nil, nil
}
func (stub *repositoryStub) CreateAccount(context.Context, bson.D) (bool, error) { return true, nil }
func (stub *repositoryStub) AccountIsActive(context.Context, string) (bool, error) {
	return stub.accountActive, nil
}
func (stub *repositoryStub) CreatePaymentChannel(_ context.Context, document bson.D) (bool, error) {
	stub.paymentChannel = document
	return true, nil
}
func (stub *repositoryStub) PaymentChannelIsActive(context.Context, string) (bool, error) {
	return stub.paymentChannelActive, nil
}
func (stub *repositoryStub) CreateTransaction(_ context.Context, document bson.D, _ string, requestHash string) (any, bool, error) {
	stub.transaction = document
	stub.transactionHash = requestHash
	return "transaction-id", true, nil
}
func (stub *repositoryStub) TransactionExists(context.Context, bson.ObjectID) (bool, error) {
	return true, nil
}
func (stub *repositoryStub) CreateEvent(_ context.Context, document bson.D, _ string, requestHash string) (any, bool, error) {
	stub.event = document
	stub.eventHash = requestHash
	return "event-id", true, nil
}
func (stub *repositoryStub) EventByID(context.Context, bson.ObjectID) (bson.M, error) {
	if len(stub.events) == 0 {
		return nil, ErrNotFound
	}
	return stub.events[0], nil
}
func (stub *repositoryStub) SearchEvents(_ context.Context, input EventSearchInput) ([]bson.M, error) {
	stub.eventSearch = input
	return stub.events, nil
}
func (stub *repositoryStub) DeleteEvent(_ context.Context, id bson.ObjectID) (bool, error) {
	stub.deletedEventID = id
	return stub.eventDeleted, nil
}
func (stub *repositoryStub) UpdateEvent(_ context.Context, _ bson.ObjectID, update bson.D, _ bool) (bson.M, error) {
	stub.eventUpdate = update
	document := bson.M{}
	for _, element := range update {
		document[element.Key] = element.Value
	}
	return document, nil
}
func (stub *repositoryStub) CreateReminder(_ context.Context, document bson.D) (bool, error) {
	stub.reminder = document
	return true, nil
}
func (stub *repositoryStub) ReminderByID(_ context.Context, id string) (ReminderRule, error) {
	for _, rule := range stub.reminderRules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return ReminderRule{}, ErrNotFound
}
func (stub *repositoryStub) ListActiveReminders(context.Context, int64) ([]ReminderRule, error) {
	return stub.reminderRules, nil
}
func (stub *repositoryStub) CompletedReminderActions(_ context.Context, _ []ReminderCompletionKey) (map[ReminderCompletionKey]bool, error) {
	return stub.completed, nil
}
func (stub *repositoryStub) CompleteReminder(_ context.Context, document bson.D, key ReminderCompletionKey) (any, bool, error) {
	stub.completion = document
	if stub.completed == nil {
		stub.completed = map[ReminderCompletionKey]bool{}
	}
	if stub.completed[key] {
		return "completion-id", false, nil
	}
	stub.completed[key] = true
	return "completion-id", true, nil
}

func TestServiceRejectsGenericManagedWrite(t *testing.T) {
	service := NewService(&repositoryStub{}, testLimits(), "Asia/Bangkok")
	for _, collection := range []string{"accounts", "payment_channels", "transactions", "events", "reminders", "reminder_completions"} {
		_, err := service.InsertOne(context.Background(), collection, bson.D{{Key: "anything", Value: true}})
		if err != ErrManagedCollection {
			t.Fatalf("expected ErrManagedCollection for %s, got %v", collection, err)
		}
	}
}

func TestServiceBuildsDirectTransaction(t *testing.T) {
	stub := &repositoryStub{accountActive: true}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	service.now = func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }
	descriptor := "  ＦＵＪＩ  "
	rawText := "Lunch at Fuji, 485 baht."

	result, err := service.CreateTransaction(context.Background(), TransactionInput{
		RequestID:       "9c3e06d5-34c0-4fc5-88cd-dd68a4db7a64",
		OccurredOn:      "2026-08-15",
		AmountMinor:     48500,
		TransactionKind: "expense",
		AccountID:       "kbank-visa",
		DescriptorRaw:   &descriptor,
		RawText:         &rawText,
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if !result.Created || result.ID != "transaction-id" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(stub.transactionHash) != 64 {
		t.Fatalf("expected SHA-256 request hash, got %q", stub.transactionHash)
	}
	source := documentValue(t, stub.transaction, "source").(bson.D)
	if documentValue(t, source, "requestHash") != stub.transactionHash {
		t.Fatal("stored request hash does not match repository argument")
	}
	descriptorDocument := documentValue(t, stub.transaction, "descriptor").(bson.D)
	normalized := documentValue(t, descriptorDocument, "normalized").(*string)
	if *normalized != "fuji" {
		t.Fatalf("unexpected descriptor normalization: %q", *normalized)
	}
}

func TestServiceCreatesPaymentChannel(t *testing.T) {
	stub := &repositoryStub{}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	service.now = func() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) }

	channel, created, err := service.CreatePaymentChannel(context.Background(), PaymentChannelInput{
		ID: "truemoney-wallet", Name: "  TrueMoney Wallet  ",
	})
	if err != nil || !created {
		t.Fatalf("create payment channel: created=%v error=%v", created, err)
	}
	if channel.ID != "truemoney-wallet" || channel.Name != "TrueMoney Wallet" || !channel.Active {
		t.Fatalf("unexpected payment channel: %#v", channel)
	}
	if documentValue(t, stub.paymentChannel, "name") != "TrueMoney Wallet" {
		t.Fatalf("unexpected stored payment channel: %#v", stub.paymentChannel)
	}
}

func TestServiceRecordsTransactionWithPaymentChannel(t *testing.T) {
	stub := &repositoryStub{accountActive: true, paymentChannelActive: true}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	descriptor := "7-Eleven"

	_, err := service.CreateTransaction(context.Background(), TransactionInput{
		RequestID: "570b5176-95c9-4812-9a2e-d52fb9b7e11a", OccurredOn: "2026-08-19",
		AmountMinor: 16500, TransactionKind: "expense", AccountID: "krungsri-homepro-credit-card",
		PaymentChannelID: "truemoney-wallet", DescriptorRaw: &descriptor,
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if documentValue(t, stub.transaction, "paymentChannelId") != "truemoney-wallet" {
		t.Fatalf("payment channel was not stored: %#v", stub.transaction)
	}
}

func TestServiceRejectsInactivePaymentChannel(t *testing.T) {
	stub := &repositoryStub{accountActive: true, paymentChannelActive: false}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	descriptor := "7-Eleven"

	_, err := service.CreateTransaction(context.Background(), TransactionInput{
		RequestID: "570b5176-95c9-4812-9a2e-d52fb9b7e11a", OccurredOn: "2026-08-19",
		AmountMinor: 16500, TransactionKind: "expense", AccountID: "krungsri-homepro-credit-card",
		PaymentChannelID: "truemoney-wallet", DescriptorRaw: &descriptor,
	})
	if err != ErrInactivePaymentChannel {
		t.Fatalf("expected ErrInactivePaymentChannel, got %v", err)
	}
}

func TestServiceRecordsNormalizedEventWithoutCreatingTransaction(t *testing.T) {
	stub := &repositoryStub{}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	service.now = func() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) }
	location := "  Sushiro Theprak branch  "
	description := "  Monthly company dinner.  "
	zero := int64(0)

	result, err := service.CreateEvent(context.Background(), EventInput{
		RequestID:   "d19f46f2-f760-4764-89ad-a22ae819ce6e",
		OccurredOn:  "2026-08-19",
		Title:       "  Company monthly dinner at Sushiro  ",
		EventType:   "company-event",
		Location:    &location,
		Description: &description,
		Tags:        []string{" Company ", "DINNER", "company"},
		FinancialContext: &EventFinancialContextInput{
			Currency: "THB", TotalValueMinor: int64Pointer(45100), AllowanceMinor: int64Pointer(50000),
			CoveredByOthersMinor: int64Pointer(45100), PersonalPaymentMinor: &zero,
		},
		Attributes: map[string]any{"restaurantChain": "Sushiro"},
		RawText:    "today มีกินเลี้ยงประจำเดือนบริษัท",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if !result.Created || result.ID != "event-id" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if documentValue(t, stub.event, "title") != "Company monthly dinner at Sushiro" {
		t.Fatalf("event title was not normalized: %#v", stub.event)
	}
	if documentValue(t, stub.event, "eventType") != "company-event" {
		t.Fatalf("unexpected event type: %#v", stub.event)
	}
	tags := documentValue(t, stub.event, "tags").([]string)
	if len(tags) != 2 || tags[0] != "company" || tags[1] != "dinner" {
		t.Fatalf("unexpected normalized tags: %#v", tags)
	}
	if len(stub.eventHash) != 64 {
		t.Fatalf("expected SHA-256 event request hash, got %q", stub.eventHash)
	}
}

func TestServiceSearchesEventsWithNormalizedFilters(t *testing.T) {
	stub := &repositoryStub{events: []bson.M{{"title": "Company monthly dinner at Sushiro"}}}
	service := NewService(stub, testLimits(), "Asia/Bangkok")

	documents, err := service.SearchEvents(context.Background(), EventSearchInput{
		OccurredFrom: "2026-08-01",
		OccurredTo:   "2026-08-31",
		Tags:         []string{" Company ", "DINNER"},
		Text:         " SUSHIRO dinner ",
	})
	if err != nil {
		t.Fatalf("search events: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("unexpected documents: %#v", documents)
	}
	if stub.eventSearch.Limit != 50 || stub.eventSearch.Sort != "occurred_desc" {
		t.Fatalf("search defaults were not applied: %#v", stub.eventSearch)
	}
	if len(stub.eventSearch.SearchTokens) != 2 || stub.eventSearch.SearchTokens[0] != "sushiro" || stub.eventSearch.SearchTokens[1] != "dinner" {
		t.Fatalf("unexpected text tokens: %#v", stub.eventSearch.SearchTokens)
	}
	if stub.eventSearch.Tags[0] != "company" || stub.eventSearch.Tags[1] != "dinner" {
		t.Fatalf("unexpected tags: %#v", stub.eventSearch.Tags)
	}
}

func TestServiceSearchEventsReturnsEmptyArray(t *testing.T) {
	service := NewService(&repositoryStub{}, testLimits(), "Asia/Bangkok")

	documents, err := service.SearchEvents(context.Background(), EventSearchInput{OccurredFrom: "2099-01-01"})
	if err != nil {
		t.Fatalf("search events: %v", err)
	}
	if documents == nil || len(documents) != 0 {
		t.Fatalf("expected a non-nil empty result, got %#v", documents)
	}
}

func TestServiceDeletesEventWithoutDeletingRelatedTransaction(t *testing.T) {
	stub := &repositoryStub{eventDeleted: true}
	service := NewService(stub, testLimits(), "Asia/Bangkok")

	err := service.DeleteEvent(context.Background(), "66c5d3d872703e1bf75d107a")
	if err != nil {
		t.Fatalf("delete event: %v", err)
	}
	if stub.deletedEventID.Hex() != "66c5d3d872703e1bf75d107a" {
		t.Fatalf("unexpected deleted event id: %s", stub.deletedEventID.Hex())
	}
}

func TestServiceUpdatesEventAndRegeneratesSearchFields(t *testing.T) {
	eventID, _ := bson.ObjectIDFromHex("66c5d3d872703e1bf75d107a")
	stub := &repositoryStub{events: []bson.M{{
		"_id": eventID, "occurredOn": "2026-08-19", "timezone": "Asia/Bangkok",
		"title": "Company dinner", "eventType": "company-event", "location": "Sushiro Theprak branch",
		"people": bson.A{}, "description": nil, "tags": bson.A{"company"}, "financialContext": nil,
		"relatedTransactionIds": bson.A{}, "attributes": bson.M{},
		"source": bson.M{"requestId": "d19f46f2-f760-4764-89ad-a22ae819ce6e", "rawText": "Company dinner."},
	}}}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	service.now = func() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC) }

	document, err := service.UpdateEvent(context.Background(), "66c5d3d872703e1bf75d107a", EventUpdateInput{
		"title": json.RawMessage(`"Company dinner at Sushiro"`),
		"tags":  json.RawMessage(`["company","sushiro"]`),
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}
	if document["title"] != "Company dinner at Sushiro" {
		t.Fatalf("unexpected updated event: %#v", document)
	}
	if documentValue(t, stub.eventUpdate, "title") != "Company dinner at Sushiro" {
		t.Fatalf("update did not contain normalized title: %#v", stub.eventUpdate)
	}
	tokens := documentValue(t, stub.eventUpdate, "searchTokens").([]string)
	if !containsString(tokens, "sushiro") {
		t.Fatalf("search tokens were not regenerated: %#v", tokens)
	}
}

func TestReminderDigestRepeatsPreparationUntilCompleted(t *testing.T) {
	preparation := &ReminderPreparationInput{Title: "Wash the company uniform", LeadDays: 2}
	stub := &repositoryStub{reminderRules: []ReminderRule{{
		ID: "company-uniform", Title: "Wear the company uniform", Timezone: "Asia/Bangkok",
		Weekdays: []string{"monday", "friday"}, StartsOn: "2026-08-17", Preparation: preparation, Active: true,
	}}}
	service := NewService(stub, testLimits(), "Asia/Bangkok")

	wednesday, err := service.ReminderDigest(context.Background(), "2026-08-19")
	if err != nil {
		t.Fatalf("Wednesday digest: %v", err)
	}
	assertDigestItem(t, wednesday, "preparation", "2026-08-21", "Wash the company uniform")

	thursday, err := service.ReminderDigest(context.Background(), "2026-08-20")
	if err != nil {
		t.Fatalf("Thursday digest: %v", err)
	}
	assertDigestItem(t, thursday, "preparation", "2026-08-21", "Wash the company uniform")

	stub.completed = map[ReminderCompletionKey]bool{{
		ReminderID: "company-uniform", OccurrenceOn: "2026-08-21", Phase: "preparation",
	}: true}
	completedThursday, err := service.ReminderDigest(context.Background(), "2026-08-20")
	if err != nil {
		t.Fatalf("completed Thursday digest: %v", err)
	}
	if len(completedThursday.Items) != 0 {
		t.Fatalf("expected completed preparation to be omitted, got %#v", completedThursday.Items)
	}

	friday, err := service.ReminderDigest(context.Background(), "2026-08-21")
	if err != nil {
		t.Fatalf("Friday digest: %v", err)
	}
	assertDigestItem(t, friday, "occurrence", "2026-08-21", "Wear the company uniform")

	saturday, err := service.ReminderDigest(context.Background(), "2026-08-22")
	if err != nil {
		t.Fatalf("Saturday digest: %v", err)
	}
	assertDigestItem(t, saturday, "preparation", "2026-08-24", "Wash the company uniform")
}

func TestCreateReminderDefaultsAndStoresRule(t *testing.T) {
	stub := &repositoryStub{}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	service.now = func() time.Time { return time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC) }

	info, created, err := service.CreateReminder(context.Background(), ReminderInput{
		ID: "company-uniform", Title: " Wear the company uniform ", Weekdays: []string{"monday", "friday"},
		Preparation: &ReminderPreparationInput{Title: " Wash the company uniform ", LeadDays: 2},
	})
	if err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	if !created || info.StartsOn != "2026-08-17" || info.Timezone != "Asia/Bangkok" {
		t.Fatalf("unexpected reminder info: %#v", info)
	}
	if documentValue(t, stub.reminder, "title") != "Wear the company uniform" {
		t.Fatalf("stored reminder was not normalized: %#v", stub.reminder)
	}
}

func TestCompleteReminderRejectsNonOccurrence(t *testing.T) {
	stub := &repositoryStub{reminderRules: []ReminderRule{{
		ID: "company-uniform", Title: "Wear the company uniform", Timezone: "Asia/Bangkok",
		Weekdays: []string{"monday", "friday"}, StartsOn: "2026-08-17", Active: true,
	}}}
	service := NewService(stub, testLimits(), "Asia/Bangkok")
	_, err := service.CompleteReminder(context.Background(), ReminderCompletionInput{
		ReminderID: "company-uniform", OccurrenceOn: "2026-08-18", Phase: "occurrence",
	})
	if err == nil {
		t.Fatal("expected a non-occurrence completion to be rejected")
	}
}

func TestReminderDigestEnforcesResultLimit(t *testing.T) {
	stub := &repositoryStub{reminderRules: []ReminderRule{{
		ID: "daily-task", Title: "Do the task", Timezone: "Asia/Bangkok",
		Weekdays: []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"},
		StartsOn: "2026-08-17", Preparation: &ReminderPreparationInput{Title: "Prepare", LeadDays: 2}, Active: true,
	}}}
	limits := testLimits()
	limits.MaxResultRecords = 1
	service := NewService(stub, limits, "Asia/Bangkok")
	_, err := service.ReminderDigest(context.Background(), "2026-08-17")
	if err == nil || !strings.Contains(err.Error(), "digest item count") {
		t.Fatalf("expected digest result limit error, got %v", err)
	}
}

func assertDigestItem(t *testing.T, digest ReminderDigest, phase, occurrenceOn, title string) {
	t.Helper()
	if len(digest.Items) != 1 {
		t.Fatalf("expected one digest item, got %#v", digest.Items)
	}
	item := digest.Items[0]
	if item.Phase != phase || item.OccurrenceOn != occurrenceOn || item.Title != title {
		t.Fatalf("unexpected digest item: %#v", item)
	}
}

func documentValue(t *testing.T, document bson.D, key string) any {
	t.Helper()
	for _, element := range document {
		if element.Key == key {
			return element.Value
		}
	}
	t.Fatalf("document does not contain %q", key)
	return nil
}

func int64Pointer(value int64) *int64 { return &value }

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func testLimits() Limits {
	return Limits{MaxCollections: 100, MaxResultRecords: 100, MaxResultBytes: 1 << 20, MaxPipelineStages: 10, OperationTimeout: time.Second}
}
