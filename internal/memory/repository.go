package memory

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Repository interface {
	Ping(context.Context) error
	EnsureBootstrap(context.Context) error
	ListCollections(context.Context) ([]string, error)
	CreateCollection(context.Context, string) error
	Find(context.Context, FindInput) ([]bson.M, error)
	FindOne(context.Context, FindInput) (bson.M, error)
	InsertOne(context.Context, string, bson.D) (any, error)
	UpdateOne(context.Context, UpdateInput) (WriteResult, error)
	DeleteOne(context.Context, DeleteInput) (WriteResult, error)
	Aggregate(context.Context, AggregateInput) ([]bson.M, error)
	CreateAccount(context.Context, bson.D) (bool, error)
	AccountIsActive(context.Context, string) (bool, error)
	CreateTransaction(context.Context, bson.D, string, string) (any, bool, error)
}
