package mongostore

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"anna/internal/memory"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type Store struct {
	client   *mongo.Client
	database *mongo.Database
}

func Connect(ctx context.Context, uri, database string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}
	store := &Store{client: client, database: client.Database(database)}
	if err := store.Ping(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return store, nil
}

func (store *Store) Close(ctx context.Context) error {
	return store.client.Disconnect(ctx)
}

func (store *Store) Ping(ctx context.Context) error {
	if err := store.client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("ping MongoDB: %w", err)
	}
	return nil
}

func (store *Store) EnsureBootstrap(ctx context.Context) error {
	existing, err := store.ListCollections(ctx)
	if err != nil {
		return err
	}
	exists := make(map[string]bool, len(existing))
	for _, name := range existing {
		exists[name] = true
	}

	collections := []struct {
		name      string
		validator bson.M
	}{
		{name: "accounts", validator: accountValidator()},
		{name: "transactions", validator: transactionValidator()},
		{name: "memories"},
		{name: "people"},
		{name: "events"},
		{name: "measurements"},
		{name: "documents"},
		{name: "reminders", validator: reminderValidator()},
		{name: "reminder_completions", validator: reminderCompletionValidator()},
	}
	for _, collection := range collections {
		if exists[collection.name] {
			continue
		}
		createOptions := options.CreateCollection()
		if collection.validator != nil {
			createOptions.SetValidator(collection.validator).SetValidationLevel("strict").SetValidationAction("error")
		}
		if err := store.database.CreateCollection(ctx, collection.name, createOptions); err != nil && !namespaceExists(err) {
			return fmt.Errorf("create bootstrap collection %q: %w", collection.name, err)
		}
	}

	if err := store.ensureIndexes(ctx); err != nil {
		return err
	}
	return nil
}

func (store *Store) ListCollections(ctx context.Context) ([]string, error) {
	names, err := store.database.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

func (store *Store) CreateCollection(ctx context.Context, name string) error {
	if err := store.database.CreateCollection(ctx, name); err != nil {
		if namespaceExists(err) {
			return memory.ErrCollectionExists
		}
		return fmt.Errorf("create collection: %w", err)
	}
	return nil
}

func (store *Store) Find(ctx context.Context, input memory.FindInput) ([]bson.M, error) {
	findOptions := options.Find().SetLimit(input.Limit)
	if len(input.Projection) > 0 {
		findOptions.SetProjection(input.Projection)
	}
	if len(input.Sort) > 0 {
		findOptions.SetSort(input.Sort)
	}
	cursor, err := store.database.Collection(input.Collection).Find(ctx, input.Filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("find documents: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []bson.M
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode documents: %w", err)
	}
	return documents, nil
}

func (store *Store) FindOne(ctx context.Context, input memory.FindInput) (bson.M, error) {
	findOptions := options.FindOne()
	if len(input.Projection) > 0 {
		findOptions.SetProjection(input.Projection)
	}
	if len(input.Sort) > 0 {
		findOptions.SetSort(input.Sort)
	}
	var document bson.M
	err := store.database.Collection(input.Collection).FindOne(ctx, input.Filter, findOptions).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, memory.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find document: %w", err)
	}
	return document, nil
}

func (store *Store) InsertOne(ctx context.Context, collection string, document bson.D) (any, error) {
	result, err := store.database.Collection(collection).InsertOne(ctx, document)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}
	return result.InsertedID, nil
}

func (store *Store) UpdateOne(ctx context.Context, input memory.UpdateInput) (memory.WriteResult, error) {
	result, err := store.database.Collection(input.Collection).UpdateOne(ctx, input.Filter, input.Update)
	if err != nil {
		return memory.WriteResult{}, fmt.Errorf("update document: %w", err)
	}
	return memory.WriteResult{MatchedCount: result.MatchedCount, ModifiedCount: result.ModifiedCount}, nil
}

func (store *Store) DeleteOne(ctx context.Context, input memory.DeleteInput) (memory.WriteResult, error) {
	result, err := store.database.Collection(input.Collection).DeleteOne(ctx, input.Filter)
	if err != nil {
		return memory.WriteResult{}, fmt.Errorf("delete document: %w", err)
	}
	return memory.WriteResult{DeletedCount: result.DeletedCount}, nil
}

func (store *Store) Aggregate(ctx context.Context, input memory.AggregateInput) ([]bson.M, error) {
	pipeline := make(mongo.Pipeline, len(input.Pipeline))
	copy(pipeline, input.Pipeline)
	cursor, err := store.database.Collection(input.Collection).Aggregate(ctx, pipeline, options.Aggregate().SetAllowDiskUse(false))
	if err != nil {
		return nil, fmt.Errorf("aggregate documents: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []bson.M
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode aggregation result: %w", err)
	}
	return documents, nil
}

func (store *Store) CreateAccount(ctx context.Context, document bson.D) (bool, error) {
	_, err := store.database.Collection("accounts").InsertOne(ctx, document)
	if mongo.IsDuplicateKeyError(err) {
		return false, memory.ErrAccountExists
	}
	if err != nil {
		return false, fmt.Errorf("create account: %w", err)
	}
	return true, nil
}

func (store *Store) AccountIsActive(ctx context.Context, id string) (bool, error) {
	count, err := store.database.Collection("accounts").CountDocuments(ctx, bson.D{
		{Key: "_id", Value: id},
		{Key: "active", Value: true},
	}, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("look up account: %w", err)
	}
	return count == 1, nil
}

func (store *Store) CreateTransaction(ctx context.Context, document bson.D, requestID, requestHash string) (any, bool, error) {
	result, err := store.database.Collection("transactions").InsertOne(ctx, document)
	if err == nil {
		return result.InsertedID, true, nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return nil, false, fmt.Errorf("create transaction: %w", err)
	}
	var existing struct {
		ID     any `bson:"_id"`
		Source struct {
			RequestHash string `bson:"requestHash"`
		} `bson:"source"`
	}
	findErr := store.database.Collection("transactions").FindOne(ctx, bson.D{{Key: "source.requestId", Value: requestID}}).Decode(&existing)
	if findErr != nil {
		return nil, false, fmt.Errorf("resolve duplicate transaction: %w", findErr)
	}
	if existing.Source.RequestHash != requestHash {
		return nil, false, memory.ErrIdempotencyConflict
	}
	return existing.ID, false, nil
}

func (store *Store) CreateReminder(ctx context.Context, document bson.D) (bool, error) {
	_, err := store.database.Collection("reminders").InsertOne(ctx, document)
	if mongo.IsDuplicateKeyError(err) {
		return false, memory.ErrReminderExists
	}
	if err != nil {
		return false, fmt.Errorf("create reminder: %w", err)
	}
	return true, nil
}

func (store *Store) ReminderByID(ctx context.Context, id string) (memory.ReminderRule, error) {
	var rule memory.ReminderRule
	err := store.database.Collection("reminders").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&rule)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return memory.ReminderRule{}, memory.ErrNotFound
	}
	if err != nil {
		return memory.ReminderRule{}, fmt.Errorf("find reminder: %w", err)
	}
	return rule, nil
}

func (store *Store) ListActiveReminders(ctx context.Context, limit int64) ([]memory.ReminderRule, error) {
	cursor, err := store.database.Collection("reminders").Find(
		ctx,
		bson.D{{Key: "active", Value: true}},
		options.Find().SetLimit(limit).SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("list active reminders: %w", err)
	}
	defer cursor.Close(ctx)
	var rules []memory.ReminderRule
	if err := cursor.All(ctx, &rules); err != nil {
		return nil, fmt.Errorf("decode reminders: %w", err)
	}
	return rules, nil
}

func (store *Store) CompletedReminderActions(ctx context.Context, keys []memory.ReminderCompletionKey) (map[memory.ReminderCompletionKey]bool, error) {
	completed := make(map[memory.ReminderCompletionKey]bool, len(keys))
	if len(keys) == 0 {
		return completed, nil
	}
	clauses := make(bson.A, 0, len(keys))
	for _, key := range keys {
		clauses = append(clauses, bson.D{
			{Key: "reminderId", Value: key.ReminderID},
			{Key: "occurrenceOn", Value: key.OccurrenceOn},
			{Key: "phase", Value: key.Phase},
		})
	}
	cursor, err := store.database.Collection("reminder_completions").Find(ctx, bson.D{{Key: "$or", Value: clauses}})
	if err != nil {
		return nil, fmt.Errorf("find reminder completions: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []struct {
		ReminderID   string `bson:"reminderId"`
		OccurrenceOn string `bson:"occurrenceOn"`
		Phase        string `bson:"phase"`
	}
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode reminder completions: %w", err)
	}
	for _, document := range documents {
		completed[memory.ReminderCompletionKey{
			ReminderID: document.ReminderID, OccurrenceOn: document.OccurrenceOn, Phase: document.Phase,
		}] = true
	}
	return completed, nil
}

func (store *Store) CompleteReminder(ctx context.Context, document bson.D, key memory.ReminderCompletionKey) (any, bool, error) {
	result, err := store.database.Collection("reminder_completions").InsertOne(ctx, document)
	if err == nil {
		return result.InsertedID, true, nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return nil, false, fmt.Errorf("complete reminder: %w", err)
	}
	var existing struct {
		ID any `bson:"_id"`
	}
	findErr := store.database.Collection("reminder_completions").FindOne(ctx, bson.D{
		{Key: "reminderId", Value: key.ReminderID},
		{Key: "occurrenceOn", Value: key.OccurrenceOn},
		{Key: "phase", Value: key.Phase},
	}).Decode(&existing)
	if findErr != nil {
		return nil, false, fmt.Errorf("resolve duplicate reminder completion: %w", findErr)
	}
	return existing.ID, false, nil
}

func (store *Store) ensureIndexes(ctx context.Context) error {
	transactionIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "source.requestId", Value: 1}},
			Options: options.Index().SetName("uniq_direct_request").SetUnique(true).SetPartialFilterExpression(
				bson.D{{Key: "source.type", Value: "direct_entry"}},
			),
		},
		{
			Keys:    bson.D{{Key: "source.importId", Value: 1}, {Key: "source.lineNumber", Value: 1}},
			Options: options.Index().SetName("uniq_import_line").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "source.type", Value: "expense_memo"}}),
		},
		{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("account_date")},
		{Keys: bson.D{{Key: "transactionKind", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("kind_date")},
		{Keys: bson.D{{Key: "categoryPath", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("category_date")},
	}
	if _, err := store.database.Collection("transactions").Indexes().CreateMany(ctx, transactionIndexes); err != nil {
		return fmt.Errorf("create transaction indexes: %w", err)
	}
	reminderIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "active", Value: 1}}, Options: options.Index().SetName("active_reminders")},
	}
	if _, err := store.database.Collection("reminders").Indexes().CreateMany(ctx, reminderIndexes); err != nil {
		return fmt.Errorf("create reminder indexes: %w", err)
	}
	completionIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "reminderId", Value: 1}, {Key: "occurrenceOn", Value: 1}, {Key: "phase", Value: 1}},
			Options: options.Index().SetName("uniq_reminder_occurrence_phase").SetUnique(true),
		},
	}
	if _, err := store.database.Collection("reminder_completions").Indexes().CreateMany(ctx, completionIndexes); err != nil {
		return fmt.Errorf("create reminder completion indexes: %w", err)
	}
	return nil
}

func namespaceExists(err error) bool {
	var commandError mongo.CommandError
	return errors.As(err, &commandError) && commandError.Code == 48
}

func accountValidator() bson.M {
	return bson.M{"$jsonSchema": bson.M{
		"bsonType":             "object",
		"additionalProperties": false,
		"required":             bson.A{"_id", "name", "kind", "currency", "active", "createdAt", "updatedAt"},
		"properties": bson.M{
			"_id":       bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"name":      bson.M{"bsonType": "string", "minLength": 1, "maxLength": 200},
			"kind":      bson.M{"enum": bson.A{"bank_account", "credit_card", "cash", "wallet", "other"}},
			"currency":  bson.M{"bsonType": "string", "pattern": "^[A-Z]{3}$"},
			"active":    bson.M{"bsonType": "bool"},
			"createdAt": bson.M{"bsonType": "date"},
			"updatedAt": bson.M{"bsonType": "date"},
		},
	}}
}

func transactionValidator() bson.M {
	nullableString := bson.M{"bsonType": bson.A{"string", "null"}}
	return bson.M{"$jsonSchema": bson.M{
		"bsonType":             "object",
		"additionalProperties": false,
		"required": bson.A{
			"_id", "schemaVersion", "occurredOn", "timezone", "amount", "transactionKind",
			"accountId", "descriptor", "resolution", "source", "createdAt", "updatedAt",
		},
		"properties": bson.M{
			"_id":           bson.M{"bsonType": "objectId"},
			"schemaVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"occurredOn":    bson.M{"bsonType": "string", "pattern": "^\\d{4}-\\d{2}-\\d{2}$"},
			"occurredAt":    bson.M{"bsonType": "date"},
			"timezone":      bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"amount": bson.M{
				"bsonType":             "object",
				"additionalProperties": false,
				"required":             bson.A{"minor", "currency"},
				"properties": bson.M{
					"minor":    bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
					"currency": bson.M{"bsonType": "string", "pattern": "^[A-Z]{3}$"},
				},
			},
			"transactionKind": bson.M{"enum": bson.A{"expense", "income", "refund", "credit_card_payment", "transfer"}},
			"accountId":       bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"descriptor": bson.M{
				"bsonType":             "object",
				"additionalProperties": false,
				"required":             bson.A{"raw", "normalized"},
				"properties": bson.M{
					"raw":        nullableString,
					"normalized": nullableString,
				},
			},
			"merchantName": nullableString,
			"categoryPath": bson.M{
				"bsonType": bson.A{"array", "null"}, "maxItems": 8,
				"items": bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			},
			"note": bson.M{"bsonType": bson.A{"string", "null"}, "maxLength": 5000},
			"resolution": bson.M{
				"bsonType": "object", "additionalProperties": false, "required": bson.A{"status"},
				"properties": bson.M{
					"status":       bson.M{"enum": bson.A{"unresolved", "alias", "manual"}},
					"aliasKey":     bson.M{"bsonType": "string", "maxLength": 200},
					"aliasVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
				},
			},
			"source": bson.M{
				"bsonType": "object", "additionalProperties": false, "required": bson.A{"type"},
				"properties": bson.M{
					"type":          bson.M{"enum": bson.A{"direct_entry", "expense_memo"}},
					"requestId":     bson.M{"bsonType": "string"},
					"requestHash":   bson.M{"bsonType": "string", "pattern": "^[a-f0-9]{64}$"},
					"rawText":       bson.M{"bsonType": bson.A{"string", "null"}, "maxLength": 5000},
					"importId":      bson.M{"bsonType": "string"},
					"lineNumber":    bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
					"rawLine":       bson.M{"bsonType": "string", "maxLength": 2000},
					"parserVersion": bson.M{"bsonType": "string", "maxLength": 100},
				},
			},
			"createdAt": bson.M{"bsonType": "date"},
			"updatedAt": bson.M{"bsonType": "date"},
		},
	}}
}

func reminderValidator() bson.M {
	return bson.M{"$jsonSchema": bson.M{
		"bsonType":             "object",
		"additionalProperties": false,
		"required": bson.A{
			"_id", "schemaVersion", "title", "timezone", "weekdays", "startsOn", "active", "createdAt", "updatedAt",
		},
		"properties": bson.M{
			"_id":           bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"schemaVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"title":         bson.M{"bsonType": "string", "minLength": 1, "maxLength": 300},
			"timezone":      bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"weekdays": bson.M{
				"bsonType": "array", "minItems": 1, "maxItems": 7, "uniqueItems": true,
				"items": bson.M{"enum": bson.A{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}},
			},
			"startsOn": bson.M{"bsonType": "string", "pattern": "^\\d{4}-\\d{2}-\\d{2}$"},
			"preparation": bson.M{
				"bsonType": "object", "additionalProperties": false, "required": bson.A{"title", "leadDays"},
				"properties": bson.M{
					"title":    bson.M{"bsonType": "string", "minLength": 1, "maxLength": 300},
					"leadDays": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1, "maximum": 30},
				},
			},
			"active":    bson.M{"bsonType": "bool"},
			"createdAt": bson.M{"bsonType": "date"},
			"updatedAt": bson.M{"bsonType": "date"},
		},
	}}
}

func reminderCompletionValidator() bson.M {
	return bson.M{"$jsonSchema": bson.M{
		"bsonType":             "object",
		"additionalProperties": false,
		"required":             bson.A{"_id", "schemaVersion", "reminderId", "occurrenceOn", "phase", "completedAt", "createdAt"},
		"properties": bson.M{
			"_id":           bson.M{"bsonType": "objectId"},
			"schemaVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"reminderId":    bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"occurrenceOn":  bson.M{"bsonType": "string", "pattern": "^\\d{4}-\\d{2}-\\d{2}$"},
			"phase":         bson.M{"enum": bson.A{"preparation", "occurrence"}},
			"completedAt":   bson.M{"bsonType": "date"},
			"createdAt":     bson.M{"bsonType": "date"},
		},
	}}
}
