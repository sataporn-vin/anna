package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type repositoryStub struct {
	collections     []string
	accountActive   bool
	transaction     bson.D
	transactionHash string
	reminder        bson.D
	reminderRules   []ReminderRule
	completed       map[ReminderCompletionKey]bool
	completion      bson.D
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
func (stub *repositoryStub) CreateTransaction(_ context.Context, document bson.D, _ string, requestHash string) (any, bool, error) {
	stub.transaction = document
	stub.transactionHash = requestHash
	return "transaction-id", true, nil
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
	_, err := service.InsertOne(context.Background(), "transactions", bson.D{{Key: "anything", Value: true}})
	if err != ErrManagedCollection {
		t.Fatalf("expected ErrManagedCollection, got %v", err)
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

func testLimits() Limits {
	return Limits{MaxCollections: 100, MaxResultRecords: 100, MaxResultBytes: 1 << 20, MaxPipelineStages: 10, OperationTimeout: time.Second}
}
