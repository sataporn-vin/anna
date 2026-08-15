package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
