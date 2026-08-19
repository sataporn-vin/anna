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
