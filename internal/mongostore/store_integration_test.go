package mongostore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"anna/internal/memory"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestManagedEventsIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI is not set")
	}
	databaseName := "anna_event_integration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Connect(ctx, uri, databaseName)
	if err != nil {
		t.Fatalf("connect to MongoDB: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = store.database.Drop(dropCtx)
		_ = store.Close(dropCtx)
	})
	if err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("ensure bootstrap: %v", err)
	}

	application := memory.NewService(store, memory.Limits{
		MaxCollections: 100, MaxResultRecords: 100, MaxResultBytes: 1 << 20,
		MaxPipelineStages: 10, OperationTimeout: 5 * time.Second,
	}, "Asia/Bangkok")
	localNow := time.Now().In(mustLocation(t, "Asia/Bangkok"))
	if _, _, err := application.CreateAccount(ctx, memory.AccountInput{
		ID: "krungsri-homepro-credit-card", Name: "Krungsri HomePro Credit Card", Kind: "credit_card", Currency: "THB",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, _, err := application.CreatePaymentChannel(ctx, memory.PaymentChannelInput{
		ID: "truemoney-wallet", Name: "TrueMoney Wallet",
	}); err != nil {
		t.Fatalf("create payment channel: %v", err)
	}
	descriptor := "7-Eleven"
	transaction, err := application.CreateTransaction(ctx, memory.TransactionInput{
		RequestID: uuid.NewString(), OccurredOn: localNow.Format("2006-01-02"), AmountMinor: 16500,
		TransactionKind: "expense", AccountID: "krungsri-homepro-credit-card",
		PaymentChannelID: "truemoney-wallet", DescriptorRaw: &descriptor,
	})
	if err != nil {
		t.Fatalf("create transaction with payment channel: %v", err)
	}
	var storedTransaction bson.M
	if err := store.database.Collection("transactions").FindOne(ctx, bson.D{{Key: "_id", Value: transaction.ID}}).Decode(&storedTransaction); err != nil {
		t.Fatalf("retrieve transaction with payment channel: %v", err)
	}
	if storedTransaction["paymentChannelId"] != "truemoney-wallet" {
		t.Fatalf("unexpected stored payment channel: %#v", storedTransaction)
	}
	requestID := uuid.NewString()
	input := memory.EventInput{
		RequestID:  requestID,
		OccurredOn: localNow.Format("2006-01-02"),
		Title:      "Company dinner at Sushiro",
		EventType:  "company-event",
		Tags:       []string{"company", "sushiro"},
		Attributes: map[string]any{"restaurantChain": "Sushiro"},
		RawText:    "The company paid for dinner at Sushiro.",
	}
	created, err := application.CreateEvent(ctx, input)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	id, ok := created.ID.(bson.ObjectID)
	if !ok {
		t.Fatalf("unexpected event id: %#v", created.ID)
	}
	retried, err := application.CreateEvent(ctx, input)
	if err != nil || retried.Created || retried.ID != id {
		t.Fatalf("retry identical event: result=%#v error=%v", retried, err)
	}
	conflicting := input
	conflicting.Title = "Different event"
	if _, err := application.CreateEvent(ctx, conflicting); !errors.Is(err, memory.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	if _, err := application.GetEvent(ctx, id.Hex()); err != nil {
		t.Fatalf("get event: %v", err)
	}
	documents, err := application.SearchEvents(ctx, memory.EventSearchInput{Text: "sushiro"})
	if err != nil {
		t.Fatalf("search events: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("expected one event, got %#v", documents)
	}
	updated, err := application.UpdateEvent(ctx, id.Hex(), memory.EventUpdateInput{
		"title": json.RawMessage(`"Updated company dinner"`),
	})
	if err != nil || updated["title"] != "Updated company dinner" {
		t.Fatalf("update event: document=%#v error=%v", updated, err)
	}

	if _, err := store.InsertOne(ctx, "events", bson.D{{Key: "title", Value: "invalid"}}); err == nil {
		t.Fatal("expected MongoDB to reject an invalid event document")
	}
	indexNames := map[string]bool{}
	cursor, err := store.database.Collection("events").Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list event indexes: %v", err)
	}
	defer cursor.Close(ctx)
	var indexes []struct {
		Name string `bson:"name"`
	}
	if err := cursor.All(ctx, &indexes); err != nil {
		t.Fatalf("decode event indexes: %v", err)
	}
	for _, index := range indexes {
		indexNames[index.Name] = true
	}
	for _, expected := range []string{"uniq_event_request", "event_date", "event_type_date", "event_tags_date", "event_location_date", "event_search_date", "event_created"} {
		if !indexNames[expected] {
			t.Fatalf("event index %q was not created: %#v", expected, indexNames)
		}
	}
	if err := application.DeleteEvent(ctx, id.Hex()); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	if _, err := application.GetEvent(ctx, id.Hex()); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("expected deleted event to be missing, got %v", err)
	}
}

func TestExistingTransactionsCollectionGetsPaymentChannelValidator(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI is not set")
	}
	databaseName := "anna_transaction_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Connect(ctx, uri, databaseName)
	if err != nil {
		t.Fatalf("connect to MongoDB: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = store.database.Drop(dropCtx)
		_ = store.Close(dropCtx)
	})
	legacyValidator := transactionValidator()
	properties := legacyValidator["$jsonSchema"].(bson.M)["properties"].(bson.M)
	delete(properties, "paymentChannelId")
	if err := store.database.CreateCollection(ctx, "transactions", options.CreateCollection().SetValidator(legacyValidator)); err != nil {
		t.Fatalf("create legacy transactions collection: %v", err)
	}
	if err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("upgrade transactions validator: %v", err)
	}
	specifications, err := store.database.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: "transactions"}})
	if err != nil || len(specifications) != 1 {
		t.Fatalf("inspect upgraded transactions validator: specs=%#v error=%v", specifications, err)
	}
	if _, err := specifications[0].Options.LookupErr("validator", "$jsonSchema", "properties", "paymentChannelId"); err != nil {
		t.Fatalf("paymentChannelId is missing from upgraded validator: %v", err)
	}
}

func TestExistingEventsCollectionGetsValidator(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI is not set")
	}
	databaseName := "anna_event_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Connect(ctx, uri, databaseName)
	if err != nil {
		t.Fatalf("connect to MongoDB: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = store.database.Drop(dropCtx)
		_ = store.Close(dropCtx)
	})
	if err := store.database.CreateCollection(ctx, "events"); err != nil {
		t.Fatalf("create legacy events collection: %v", err)
	}
	if err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("upgrade events collection: %v", err)
	}
	if _, err := store.InsertOne(ctx, "events", bson.D{{Key: "title", Value: "invalid"}}); err == nil {
		t.Fatal("expected upgraded validator to reject an invalid event")
	}
}

func TestExistingIncompatibleEventStopsValidatorMigration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI is not set")
	}
	databaseName := "anna_event_incompatible_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Connect(ctx, uri, databaseName)
	if err != nil {
		t.Fatalf("connect to MongoDB: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = store.database.Drop(dropCtx)
		_ = store.Close(dropCtx)
	})
	if _, err := store.InsertOne(ctx, "events", bson.D{{Key: "legacy", Value: true}}); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	err = store.EnsureBootstrap(ctx)
	if err == nil || !strings.Contains(err.Error(), "incompatible with schema version 1") {
		t.Fatalf("expected actionable migration error, got %v", err)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	return location
}
