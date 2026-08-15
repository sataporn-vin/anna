package memory

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type repositoryStub struct {
	collections     []string
	accountActive   bool
	transaction     bson.D
	transactionHash string
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
