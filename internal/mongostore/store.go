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
		{name: "events", validator: eventValidator()},
		{name: "measurements"},
		{name: "documents"},
		{name: "reminders", validator: reminderValidator()},
		{name: "reminder_completions", validator: reminderCompletionValidator()},
	}
	for _, collection := range collections {
		if exists[collection.name] {
			if collection.name == "events" {
				if err := store.ensureExistingEventValidator(ctx); err != nil {
					return err
				}
			}
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

func (store *Store) ensureExistingEventValidator(ctx context.Context) error {
	specifications, err := store.database.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: "events"}})
	if err != nil {
		return fmt.Errorf("inspect events collection validator: %w", err)
	}
	if len(specifications) != 1 {
		return fmt.Errorf("inspect events collection validator: expected one events collection")
	}
	if _, err := specifications[0].Options.LookupErr("validator"); err == nil {
		return nil
	}
	schema := eventValidator()["$jsonSchema"]
	invalid, err := store.database.Collection("events").CountDocuments(ctx, bson.D{{Key: "$nor", Value: bson.A{bson.D{{Key: "$jsonSchema", Value: schema}}}}}, options.Count().SetLimit(1))
	if err != nil {
		return fmt.Errorf("validate existing events before migration: %w", err)
	}
	if invalid != 0 {
		return fmt.Errorf("events collection contains documents incompatible with schema version 1; migrate or remove them before startup")
	}
	command := bson.D{
		{Key: "collMod", Value: "events"},
		{Key: "validator", Value: eventValidator()},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	}
	if err := store.database.RunCommand(ctx, command).Err(); err != nil {
		return fmt.Errorf("apply events collection validator (the database user needs collMod permission for this one-time upgrade): %w", err)
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

func (store *Store) TransactionExists(ctx context.Context, id bson.ObjectID) (bool, error) {
	count, err := store.database.Collection("transactions").CountDocuments(ctx, bson.D{{Key: "_id", Value: id}}, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("look up transaction: %w", err)
	}
	return count == 1, nil
}

func (store *Store) CreateEvent(ctx context.Context, document bson.D, requestID, requestHash string) (any, bool, error) {
	result, err := store.database.Collection("events").InsertOne(ctx, document)
	if err == nil {
		return result.InsertedID, true, nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return nil, false, fmt.Errorf("create event: %w", err)
	}
	var existing struct {
		ID     any `bson:"_id"`
		Source struct {
			RequestHash string `bson:"requestHash"`
		} `bson:"source"`
	}
	findErr := store.database.Collection("events").FindOne(ctx, bson.D{{Key: "source.requestId", Value: requestID}}).Decode(&existing)
	if findErr != nil {
		return nil, false, fmt.Errorf("resolve duplicate event: %w", findErr)
	}
	if existing.Source.RequestHash != requestHash {
		return nil, false, memory.ErrIdempotencyConflict
	}
	return existing.ID, false, nil
}

func (store *Store) EventByID(ctx context.Context, id bson.ObjectID) (bson.M, error) {
	var document bson.M
	err := store.database.Collection("events").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, memory.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find event: %w", err)
	}
	return document, nil
}

func (store *Store) SearchEvents(ctx context.Context, input memory.EventSearchInput) ([]bson.M, error) {
	filter := bson.D{}
	if input.OccurredFrom != "" || input.OccurredTo != "" {
		rangeFilter := bson.D{}
		if input.OccurredFrom != "" {
			rangeFilter = append(rangeFilter, bson.E{Key: "$gte", Value: input.OccurredFrom})
		}
		if input.OccurredTo != "" {
			rangeFilter = append(rangeFilter, bson.E{Key: "$lte", Value: input.OccurredTo})
		}
		filter = append(filter, bson.E{Key: "occurredOn", Value: rangeFilter})
	}
	if input.CreatedFrom != nil || input.CreatedTo != nil {
		rangeFilter := bson.D{}
		if input.CreatedFrom != nil {
			rangeFilter = append(rangeFilter, bson.E{Key: "$gte", Value: input.CreatedFrom.UTC()})
		}
		if input.CreatedTo != nil {
			rangeFilter = append(rangeFilter, bson.E{Key: "$lt", Value: input.CreatedTo.UTC()})
		}
		filter = append(filter, bson.E{Key: "createdAt", Value: rangeFilter})
	}
	if len(input.EventTypes) > 0 {
		filter = append(filter, bson.E{Key: "eventType", Value: bson.D{{Key: "$in", Value: input.EventTypes}}})
	}
	if input.Location != "" {
		filter = append(filter, bson.E{Key: "locationNormalized", Value: input.Location})
	}
	if len(input.People) > 0 {
		filter = append(filter, bson.E{Key: "peopleNormalized", Value: bson.D{{Key: "$all", Value: input.People}}})
	}
	if len(input.Tags) > 0 {
		filter = append(filter, bson.E{Key: "tags", Value: bson.D{{Key: "$all", Value: input.Tags}}})
	}
	if len(input.SearchTokens) > 0 {
		filter = append(filter, bson.E{Key: "searchTokens", Value: bson.D{{Key: "$all", Value: input.SearchTokens}}})
	}
	sortDocument := bson.D{{Key: "occurredOn", Value: -1}, {Key: "_id", Value: -1}}
	if input.Sort == "occurred_asc" {
		sortDocument = bson.D{{Key: "occurredOn", Value: 1}, {Key: "_id", Value: 1}}
	} else if input.Sort == "created_desc" {
		sortDocument = bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}
	}
	cursor, err := store.database.Collection("events").Find(ctx, filter, options.Find().SetLimit(input.Limit).SetSort(sortDocument))
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []bson.M
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return documents, nil
}

func (store *Store) DeleteEvent(ctx context.Context, id bson.ObjectID) (bool, error) {
	result, err := store.database.Collection("events").DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return false, fmt.Errorf("delete event: %w", err)
	}
	return result.DeletedCount == 1, nil
}

func (store *Store) UpdateEvent(ctx context.Context, id bson.ObjectID, fields bson.D, unsetOccurredAt bool) (bson.M, error) {
	update := bson.D{{Key: "$set", Value: fields}}
	if unsetOccurredAt {
		update = append(update, bson.E{Key: "$unset", Value: bson.D{{Key: "occurredAt", Value: ""}}})
	}
	var document bson.M
	err := store.database.Collection("events").FindOneAndUpdate(
		ctx,
		bson.D{{Key: "_id", Value: id}},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, memory.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update event: %w", err)
	}
	return document, nil
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
	eventIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "source.requestId", Value: 1}},
			Options: options.Index().SetName("uniq_event_request").SetUnique(true).SetPartialFilterExpression(
				bson.D{{Key: "source.type", Value: "direct_entry"}},
			),
		},
		{Keys: bson.D{{Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("event_date")},
		{Keys: bson.D{{Key: "eventType", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("event_type_date")},
		{Keys: bson.D{{Key: "tags", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("event_tags_date")},
		{Keys: bson.D{{Key: "locationNormalized", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("event_location_date")},
		{Keys: bson.D{{Key: "searchTokens", Value: 1}, {Key: "occurredOn", Value: -1}}, Options: options.Index().SetName("event_search_date")},
		{Keys: bson.D{{Key: "createdAt", Value: -1}}, Options: options.Index().SetName("event_created")},
	}
	if _, err := store.database.Collection("events").Indexes().CreateMany(ctx, eventIndexes); err != nil {
		return fmt.Errorf("create event indexes: %w", err)
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

func eventValidator() bson.M {
	nullableString := bson.M{"bsonType": bson.A{"string", "null"}}
	nullableMoney := bson.M{"bsonType": bson.A{"int", "long", "null"}, "minimum": 0}
	attributeScalarTypes := bson.A{"string", "bool", "int", "long", "double", "decimal", "null"}
	return bson.M{"$jsonSchema": bson.M{
		"bsonType":             "object",
		"additionalProperties": false,
		"required": bson.A{
			"_id", "schemaVersion", "occurredOn", "timezone", "title", "eventType", "location",
			"locationNormalized", "people", "peopleNormalized", "description", "tags", "financialContext",
			"relatedTransactionIds", "attributes", "source", "searchTokens", "createdAt", "updatedAt",
		},
		"properties": bson.M{
			"_id":           bson.M{"bsonType": "objectId"},
			"schemaVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1, "maximum": 1},
			"occurredOn":    bson.M{"bsonType": "string", "pattern": "^\\d{4}-\\d{2}-\\d{2}$"},
			"occurredAt":    bson.M{"bsonType": "date"},
			"timezone":      bson.M{"bsonType": "string", "minLength": 1, "maxLength": 100},
			"title":         bson.M{"bsonType": "string", "minLength": 1, "maxLength": 300},
			"eventType": bson.M{"enum": bson.A{
				"company-event", "social-event", "meal-gathering", "appointment-completed", "travel", "celebration",
				"personal-milestone", "work-milestone", "health-fitness", "pet", "family", "general-memory",
			}},
			"location":           nullableString,
			"locationNormalized": nullableString,
			"people": bson.M{
				"bsonType": "array", "maxItems": 50,
				"items": bson.M{"bsonType": "string", "minLength": 1, "maxLength": 200},
			},
			"peopleNormalized": bson.M{
				"bsonType": "array", "maxItems": 50,
				"items": bson.M{"bsonType": "string", "minLength": 1, "maxLength": 200},
			},
			"description": bson.M{"bsonType": bson.A{"string", "null"}, "maxLength": 5000},
			"tags": bson.M{
				"bsonType": "array", "maxItems": 20, "uniqueItems": true,
				"items": bson.M{"bsonType": "string", "minLength": 1, "maxLength": 50},
			},
			"financialContext": bson.M{
				"bsonType": bson.A{"object", "null"}, "additionalProperties": false,
				"required": bson.A{"currency", "totalValueMinor", "allowanceMinor", "coveredByOthersMinor", "personalPaymentMinor"},
				"properties": bson.M{
					"currency":             bson.M{"bsonType": "string", "pattern": "^[A-Z]{3}$"},
					"totalValueMinor":      nullableMoney,
					"allowanceMinor":       nullableMoney,
					"coveredByOthersMinor": nullableMoney,
					"personalPaymentMinor": nullableMoney,
				},
			},
			"relatedTransactionIds": bson.M{
				"bsonType": "array", "maxItems": 20, "uniqueItems": true,
				"items": bson.M{"bsonType": "objectId"},
			},
			"attributes": bson.M{
				"bsonType": bson.A{"object", "null"}, "maxProperties": 32,
				"additionalProperties": bson.M{"anyOf": bson.A{
					bson.M{"bsonType": attributeScalarTypes, "maxLength": 1000},
					bson.M{"bsonType": "array", "maxItems": 20, "items": bson.M{"bsonType": attributeScalarTypes, "maxLength": 1000}},
				}},
			},
			"source": bson.M{
				"bsonType": "object", "additionalProperties": false,
				"required": bson.A{"type", "requestId", "requestHash", "rawText"},
				"properties": bson.M{
					"type":        bson.M{"enum": bson.A{"direct_entry"}},
					"requestId":   bson.M{"bsonType": "string", "minLength": 36, "maxLength": 36},
					"requestHash": bson.M{"bsonType": "string", "pattern": "^[a-f0-9]{64}$"},
					"rawText":     bson.M{"bsonType": "string", "minLength": 1, "maxLength": 5000},
				},
			},
			"searchTokens": bson.M{
				"bsonType": "array", "maxItems": 100, "uniqueItems": true,
				"items": bson.M{"bsonType": "string", "minLength": 1, "maxLength": 500},
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
