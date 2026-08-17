package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var bootstrapCollections = []string{
	"accounts",
	"transactions",
	"memories",
	"people",
	"events",
	"measurements",
	"documents",
	"reminders",
	"reminder_completions",
}

type Service struct {
	repository      Repository
	limits          Limits
	defaultTimezone string
	now             func() time.Time
}

func NewService(repository Repository, limits Limits, defaultTimezone string) *Service {
	return &Service{
		repository:      repository,
		limits:          limits,
		defaultTimezone: defaultTimezone,
		now:             time.Now,
	}
}

func (service *Service) EnsureBootstrap(ctx context.Context) error {
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	return service.repository.EnsureBootstrap(ctx)
}

func (service *Service) Ping(ctx context.Context) error {
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	return service.repository.Ping(ctx)
}

func (service *Service) ListCollections(ctx context.Context) ([]CollectionInfo, error) {
	ctx, cancel := service.operationContext(ctx)
	defer cancel()

	names, err := service.repository.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	collections := make([]CollectionInfo, 0, len(names))
	for _, name := range names {
		if IsReservedCollection(name) || !collectionNamePattern.MatchString(name) {
			continue
		}
		collections = append(collections, CollectionInfo{Name: name, Managed: IsManagedCollection(name)})
	}
	return collections, nil
}

func (service *Service) CreateCollection(ctx context.Context, name string) (CollectionInfo, bool, error) {
	if err := ValidateCollectionName(name); err != nil {
		return CollectionInfo{}, false, Invalid(err)
	}
	if IsManagedCollection(name) {
		return CollectionInfo{}, false, fmt.Errorf("collection %q is managed by the service", name)
	}

	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	names, err := service.repository.ListCollections(ctx)
	if err != nil {
		return CollectionInfo{}, false, err
	}
	for _, existing := range names {
		if existing == name {
			return CollectionInfo{Name: name}, false, nil
		}
	}
	if len(names) >= service.limits.MaxCollections {
		return CollectionInfo{}, false, ErrCollectionLimit
	}
	if err := service.repository.CreateCollection(ctx, name); err != nil {
		if err == ErrCollectionExists {
			return CollectionInfo{Name: name}, false, nil
		}
		return CollectionInfo{}, false, err
	}
	return CollectionInfo{Name: name}, true, nil
}

func (service *Service) Find(ctx context.Context, input FindInput) ([]bson.M, error) {
	if err := service.validateReadInput(ctx, &input); err != nil {
		return nil, err
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	documents, err := service.repository.Find(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := service.validateResult(documents); err != nil {
		return nil, err
	}
	return documents, nil
}

func (service *Service) FindOne(ctx context.Context, input FindInput) (bson.M, error) {
	if err := service.validateReadInput(ctx, &input); err != nil {
		return nil, err
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	document, err := service.repository.FindOne(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := service.validateResult([]bson.M{document}); err != nil {
		return nil, err
	}
	return document, nil
}

func (service *Service) InsertOne(ctx context.Context, collection string, document bson.D) (WriteResult, error) {
	if err := service.validateGenericWrite(ctx, collection); err != nil {
		return WriteResult{}, err
	}
	if err := ValidateInsert(document); err != nil {
		return WriteResult{}, Invalid(err)
	}
	now := service.now().UTC()
	document = append(document,
		bson.E{Key: "createdAt", Value: now},
		bson.E{Key: "updatedAt", Value: now},
	)
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	id, err := service.repository.InsertOne(ctx, collection, document)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{ID: id, Created: true}, nil
}

func (service *Service) UpdateOne(ctx context.Context, input UpdateInput) (WriteResult, error) {
	if err := service.validateGenericWrite(ctx, input.Collection); err != nil {
		return WriteResult{}, err
	}
	if err := ValidateFilter(input.Filter, false); err != nil {
		return WriteResult{}, Invalid(err)
	}
	if err := ValidateUpdate(input.Update); err != nil {
		return WriteResult{}, Invalid(err)
	}
	input.Update = setUpdatedAt(input.Update, service.now().UTC())
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	return service.repository.UpdateOne(ctx, input)
}

func (service *Service) DeleteOne(ctx context.Context, input DeleteInput) (WriteResult, error) {
	if err := service.validateGenericWrite(ctx, input.Collection); err != nil {
		return WriteResult{}, err
	}
	if err := ValidateFilter(input.Filter, false); err != nil {
		return WriteResult{}, Invalid(err)
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	return service.repository.DeleteOne(ctx, input)
}

func (service *Service) Aggregate(ctx context.Context, input AggregateInput) ([]bson.M, error) {
	if err := service.ensureAccessible(ctx, input.Collection); err != nil {
		return nil, err
	}
	if err := ValidatePipeline(input.Pipeline, service.limits.MaxPipelineStages); err != nil {
		return nil, Invalid(err)
	}
	input.Pipeline = append(input.Pipeline, bson.D{{Key: "$limit", Value: service.limits.MaxResultRecords + 1}})
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	documents, err := service.repository.Aggregate(ctx, input)
	if err != nil {
		return nil, err
	}
	if int64(len(documents)) > service.limits.MaxResultRecords {
		return nil, fmt.Errorf("%w: aggregation record count", ErrResultLimit)
	}
	if err := service.validateResult(documents); err != nil {
		return nil, err
	}
	return documents, nil
}

func (service *Service) CreateAccount(ctx context.Context, input AccountInput) (AccountInfo, bool, error) {
	if input.Currency == "" {
		input.Currency = "THB"
	}
	if err := ValidateAccount(input); err != nil {
		return AccountInfo{}, false, Invalid(err)
	}
	now := service.now().UTC()
	document := bson.D{
		{Key: "_id", Value: input.ID},
		{Key: "name", Value: strings.TrimSpace(input.Name)},
		{Key: "kind", Value: input.Kind},
		{Key: "currency", Value: input.Currency},
		{Key: "active", Value: true},
		{Key: "createdAt", Value: now},
		{Key: "updatedAt", Value: now},
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	created, err := service.repository.CreateAccount(ctx, document)
	return AccountInfo{ID: input.ID, Name: strings.TrimSpace(input.Name), Kind: input.Kind, Currency: input.Currency, Active: true}, created, err
}

func (service *Service) CreateTransaction(ctx context.Context, input TransactionInput) (WriteResult, error) {
	if err := ValidateTransaction(&input, service.defaultTimezone); err != nil {
		return WriteResult{}, Invalid(err)
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	active, err := service.repository.AccountIsActive(ctx, input.AccountID)
	if err != nil {
		return WriteResult{}, err
	}
	if !active {
		return WriteResult{}, ErrInactiveAccount
	}
	requestBytes, err := json.Marshal(input)
	if err != nil {
		return WriteResult{}, fmt.Errorf("encode transaction idempotency input: %w", err)
	}
	requestDigest := sha256.Sum256(requestBytes)
	requestHash := hex.EncodeToString(requestDigest[:])

	now := service.now().UTC()
	descriptorNormalized := NormalizeDescriptor(input.DescriptorRaw)
	document := bson.D{
		{Key: "schemaVersion", Value: int32(1)},
		{Key: "occurredOn", Value: input.OccurredOn},
		{Key: "timezone", Value: input.Timezone},
		{Key: "amount", Value: bson.D{
			{Key: "minor", Value: input.AmountMinor},
			{Key: "currency", Value: input.Currency},
		}},
		{Key: "transactionKind", Value: input.TransactionKind},
		{Key: "accountId", Value: input.AccountID},
		{Key: "descriptor", Value: bson.D{
			{Key: "raw", Value: input.DescriptorRaw},
			{Key: "normalized", Value: descriptorNormalized},
		}},
		{Key: "merchantName", Value: input.MerchantName},
		{Key: "categoryPath", Value: input.CategoryPath},
		{Key: "note", Value: input.Note},
		{Key: "resolution", Value: bson.D{{Key: "status", Value: "manual"}}},
		{Key: "source", Value: bson.D{
			{Key: "type", Value: "direct_entry"},
			{Key: "requestId", Value: input.RequestID},
			{Key: "requestHash", Value: requestHash},
			{Key: "rawText", Value: input.RawText},
		}},
		{Key: "createdAt", Value: now},
		{Key: "updatedAt", Value: now},
	}
	if input.OccurredAt != nil {
		document = append(document[:3], append(bson.D{{Key: "occurredAt", Value: input.OccurredAt.UTC()}}, document[3:]...)...)
	}
	id, created, err := service.repository.CreateTransaction(ctx, document, input.RequestID, requestHash)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{ID: id, Created: created}, nil
}

func (service *Service) CreateReminder(ctx context.Context, input ReminderInput) (ReminderInfo, bool, error) {
	now := service.now()
	if err := ValidateReminder(&input, service.defaultTimezone, now); err != nil {
		return ReminderInfo{}, false, Invalid(err)
	}
	now = now.UTC()
	document := bson.D{
		{Key: "_id", Value: input.ID},
		{Key: "schemaVersion", Value: int32(1)},
		{Key: "title", Value: input.Title},
		{Key: "timezone", Value: input.Timezone},
		{Key: "weekdays", Value: input.Weekdays},
		{Key: "startsOn", Value: input.StartsOn},
		{Key: "active", Value: true},
		{Key: "createdAt", Value: now},
		{Key: "updatedAt", Value: now},
	}
	if input.Preparation != nil {
		document = append(document[:6], append(bson.D{{Key: "preparation", Value: input.Preparation}}, document[6:]...)...)
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	created, err := service.repository.CreateReminder(ctx, document)
	info := ReminderInfo{
		ID: input.ID, Title: input.Title, Timezone: input.Timezone, Weekdays: input.Weekdays,
		StartsOn: input.StartsOn, Preparation: input.Preparation, Active: true,
	}
	return info, created, err
}

func (service *Service) ReminderDigest(ctx context.Context, on string) (ReminderDigest, error) {
	if on == "" {
		location, err := time.LoadLocation(service.defaultTimezone)
		if err != nil {
			return ReminderDigest{}, fmt.Errorf("load default timezone: %w", err)
		}
		on = service.now().In(location).Format("2006-01-02")
	}
	if _, err := parseDate(on, time.UTC); err != nil {
		return ReminderDigest{}, Invalid(fmt.Errorf("on must be a real date in YYYY-MM-DD format"))
	}

	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	rules, err := service.repository.ListActiveReminders(ctx, service.limits.MaxResultRecords+1)
	if err != nil {
		return ReminderDigest{}, err
	}
	if int64(len(rules)) > service.limits.MaxResultRecords {
		return ReminderDigest{}, fmt.Errorf("%w: active reminder count", ErrResultLimit)
	}

	items := make([]ReminderDigestItem, 0)
	keys := make([]ReminderCompletionKey, 0)
	for _, rule := range rules {
		location, err := time.LoadLocation(rule.Timezone)
		if err != nil {
			return ReminderDigest{}, fmt.Errorf("load timezone for reminder %q: %w", rule.ID, err)
		}
		digestDate, err := parseDate(on, location)
		if err != nil {
			return ReminderDigest{}, Invalid(fmt.Errorf("on must be a real date in YYYY-MM-DD format"))
		}
		startsOn, err := parseDate(rule.StartsOn, location)
		if err != nil {
			return ReminderDigest{}, fmt.Errorf("reminder %q has an invalid startsOn date", rule.ID)
		}

		if !digestDate.Before(startsOn) && reminderOccursOn(rule, digestDate) {
			item := ReminderDigestItem{ReminderID: rule.ID, OccurrenceOn: on, Phase: "occurrence", Title: rule.Title}
			items = append(items, item)
			keys = append(keys, completionKey(item))
		}
		if rule.Preparation == nil {
			continue
		}
		for daysAhead := 1; daysAhead <= rule.Preparation.LeadDays; daysAhead++ {
			occurrence := digestDate.AddDate(0, 0, daysAhead)
			if occurrence.Before(startsOn) || !reminderOccursOn(rule, occurrence) {
				continue
			}
			item := ReminderDigestItem{
				ReminderID: rule.ID, OccurrenceOn: occurrence.Format("2006-01-02"),
				Phase: "preparation", Title: rule.Preparation.Title,
			}
			items = append(items, item)
			keys = append(keys, completionKey(item))
		}
	}

	completed, err := service.repository.CompletedReminderActions(ctx, keys)
	if err != nil {
		return ReminderDigest{}, err
	}
	open := make([]ReminderDigestItem, 0, len(items))
	for _, item := range items {
		if !completed[completionKey(item)] {
			open = append(open, item)
		}
	}
	sort.Slice(open, func(left, right int) bool {
		if open[left].OccurrenceOn != open[right].OccurrenceOn {
			return open[left].OccurrenceOn < open[right].OccurrenceOn
		}
		if open[left].Phase != open[right].Phase {
			return open[left].Phase == "occurrence"
		}
		return open[left].ReminderID < open[right].ReminderID
	})
	if int64(len(open)) > service.limits.MaxResultRecords {
		return ReminderDigest{}, fmt.Errorf("%w: digest item count", ErrResultLimit)
	}
	digest := ReminderDigest{On: on, Items: open}
	data, err := json.Marshal(digest)
	if err != nil {
		return ReminderDigest{}, fmt.Errorf("encode reminder digest: %w", err)
	}
	if len(data) > service.limits.MaxResultBytes {
		return ReminderDigest{}, fmt.Errorf("%w: digest byte count", ErrResultLimit)
	}
	return digest, nil
}

func (service *Service) CompleteReminder(ctx context.Context, input ReminderCompletionInput) (WriteResult, error) {
	if err := ValidateReminderCompletion(input); err != nil {
		return WriteResult{}, Invalid(err)
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	rule, err := service.repository.ReminderByID(ctx, input.ReminderID)
	if err != nil {
		return WriteResult{}, err
	}
	location, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		return WriteResult{}, fmt.Errorf("load reminder timezone: %w", err)
	}
	occurrence, err := parseDate(input.OccurrenceOn, location)
	if err != nil {
		return WriteResult{}, Invalid(fmt.Errorf("occurrenceOn must be a real date in YYYY-MM-DD format"))
	}
	startsOn, err := parseDate(rule.StartsOn, location)
	if err != nil {
		return WriteResult{}, fmt.Errorf("reminder has an invalid startsOn date")
	}
	if occurrence.Before(startsOn) || !reminderOccursOn(rule, occurrence) {
		return WriteResult{}, Invalid(fmt.Errorf("occurrenceOn is not an occurrence of this reminder"))
	}
	if input.Phase == "preparation" && rule.Preparation == nil {
		return WriteResult{}, Invalid(fmt.Errorf("this reminder has no preparation step"))
	}
	key := ReminderCompletionKey{ReminderID: input.ReminderID, OccurrenceOn: input.OccurrenceOn, Phase: input.Phase}
	now := service.now().UTC()
	document := bson.D{
		{Key: "schemaVersion", Value: int32(1)},
		{Key: "reminderId", Value: input.ReminderID},
		{Key: "occurrenceOn", Value: input.OccurrenceOn},
		{Key: "phase", Value: input.Phase},
		{Key: "completedAt", Value: now},
		{Key: "createdAt", Value: now},
	}
	id, created, err := service.repository.CompleteReminder(ctx, document, key)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{ID: id, Created: created}, nil
}

func reminderOccursOn(rule ReminderRule, date time.Time) bool {
	weekday := strings.ToLower(date.Weekday().String())
	for _, scheduled := range rule.Weekdays {
		if scheduled == weekday {
			return true
		}
	}
	return false
}

func completionKey(item ReminderDigestItem) ReminderCompletionKey {
	return ReminderCompletionKey{ReminderID: item.ReminderID, OccurrenceOn: item.OccurrenceOn, Phase: item.Phase}
}

func (service *Service) validateReadInput(ctx context.Context, input *FindInput) error {
	if err := service.ensureAccessible(ctx, input.Collection); err != nil {
		return err
	}
	if err := ValidateFilter(input.Filter, true); err != nil {
		return Invalid(err)
	}
	if err := ValidateProjection(input.Projection); err != nil {
		return Invalid(err)
	}
	if err := ValidateSort(input.Sort); err != nil {
		return Invalid(err)
	}
	if input.Limit == 0 {
		input.Limit = 50
	}
	if input.Limit < 1 || input.Limit > service.limits.MaxResultRecords {
		return Invalid(fmt.Errorf("limit must be between 1 and %d", service.limits.MaxResultRecords))
	}
	return nil
}

func (service *Service) validateGenericWrite(ctx context.Context, collection string) error {
	if IsManagedCollection(collection) {
		return ErrManagedCollection
	}
	return service.ensureAccessible(ctx, collection)
}

func (service *Service) ensureAccessible(ctx context.Context, collection string) error {
	if err := ValidateCollectionName(collection); err != nil {
		return Invalid(err)
	}
	ctx, cancel := service.operationContext(ctx)
	defer cancel()
	names, err := service.repository.ListCollections(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == collection {
			return nil
		}
	}
	return ErrNotFound
}

func (service *Service) validateResult(documents []bson.M) error {
	if int64(len(documents)) > service.limits.MaxResultRecords {
		return fmt.Errorf("%w: record count", ErrResultLimit)
	}
	data, err := bson.MarshalExtJSON(bson.M{"data": documents}, false, false)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	if len(data) > service.limits.MaxResultBytes {
		return fmt.Errorf("%w: encoded byte count", ErrResultLimit)
	}
	return nil
}

func (service *Service) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, service.limits.OperationTimeout)
}

func setUpdatedAt(update bson.D, now time.Time) bson.D {
	for index := range update {
		if update[index].Key == "$set" {
			fields, _ := asDocument(update[index].Value)
			fields = append(fields, bson.E{Key: "updatedAt", Value: now})
			update[index].Value = fields
			return update
		}
	}
	return append(update, bson.E{Key: "$set", Value: bson.D{{Key: "updatedAt", Value: now}}})
}
