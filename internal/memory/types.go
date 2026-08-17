package memory

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrCollectionExists    = errors.New("collection already exists")
	ErrCollectionLimit     = errors.New("collection limit reached")
	ErrManagedCollection   = errors.New("managed collection does not allow generic writes")
	ErrInactiveAccount     = errors.New("account does not exist or is inactive")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for different content")
	ErrAccountExists       = errors.New("account already exists")
	ErrReminderExists      = errors.New("reminder already exists")
	ErrResultLimit         = errors.New("result exceeds a configured limit")
)

type ValidationError struct {
	Err error
}

func (err ValidationError) Error() string { return err.Err.Error() }
func (err ValidationError) Unwrap() error { return err.Err }

func Invalid(err error) error {
	if err == nil {
		return nil
	}
	return ValidationError{Err: err}
}

type Limits struct {
	MaxCollections    int
	MaxResultRecords  int64
	MaxResultBytes    int
	MaxPipelineStages int
	OperationTimeout  time.Duration
}

type FindInput struct {
	Collection string
	Filter     bson.D
	Projection bson.D
	Sort       bson.D
	Limit      int64
}

type UpdateInput struct {
	Collection string
	Filter     bson.D
	Update     bson.D
}

type DeleteInput struct {
	Collection string
	Filter     bson.D
}

type AggregateInput struct {
	Collection string
	Pipeline   []bson.D
}

type WriteResult struct {
	ID            any   `json:"id,omitempty"`
	MatchedCount  int64 `json:"matchedCount,omitempty"`
	ModifiedCount int64 `json:"modifiedCount,omitempty"`
	DeletedCount  int64 `json:"deletedCount,omitempty"`
	Created       bool  `json:"created,omitempty"`
}

type AccountInput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
}

type AccountInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Active   bool   `json:"active"`
}

type TransactionInput struct {
	RequestID       string     `json:"requestId"`
	OccurredOn      string     `json:"occurredOn"`
	OccurredAt      *time.Time `json:"occurredAt,omitempty"`
	Timezone        string     `json:"timezone,omitempty"`
	AmountMinor     int64      `json:"amountMinor"`
	Currency        string     `json:"currency,omitempty"`
	TransactionKind string     `json:"transactionKind"`
	AccountID       string     `json:"accountId"`
	DescriptorRaw   *string    `json:"descriptorRaw,omitempty"`
	MerchantName    *string    `json:"merchantName,omitempty"`
	CategoryPath    []string   `json:"categoryPath,omitempty"`
	Note            *string    `json:"note,omitempty"`
	RawText         *string    `json:"rawText,omitempty"`
}

type CollectionInfo struct {
	Name    string `json:"name"`
	Managed bool   `json:"managed"`
}

type ReminderPreparationInput struct {
	Title    string `json:"title" bson:"title"`
	LeadDays int    `json:"leadDays" bson:"leadDays"`
}

type ReminderInput struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Timezone    string                    `json:"timezone,omitempty"`
	Weekdays    []string                  `json:"weekdays"`
	StartsOn    string                    `json:"startsOn,omitempty"`
	Preparation *ReminderPreparationInput `json:"preparation,omitempty"`
}

type ReminderInfo struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Timezone    string                    `json:"timezone"`
	Weekdays    []string                  `json:"weekdays"`
	StartsOn    string                    `json:"startsOn"`
	Preparation *ReminderPreparationInput `json:"preparation,omitempty"`
	Active      bool                      `json:"active"`
}

type ReminderRule struct {
	ID          string                    `bson:"_id"`
	Title       string                    `bson:"title"`
	Timezone    string                    `bson:"timezone"`
	Weekdays    []string                  `bson:"weekdays"`
	StartsOn    string                    `bson:"startsOn"`
	Preparation *ReminderPreparationInput `bson:"preparation,omitempty"`
	Active      bool                      `bson:"active"`
}

type ReminderCompletionInput struct {
	ReminderID   string `json:"reminderId"`
	OccurrenceOn string `json:"occurrenceOn"`
	Phase        string `json:"phase"`
}

type ReminderCompletionKey struct {
	ReminderID   string
	OccurrenceOn string
	Phase        string
}

type ReminderDigestItem struct {
	ReminderID   string `json:"reminderId"`
	OccurrenceOn string `json:"occurrenceOn"`
	Phase        string `json:"phase"`
	Title        string `json:"title"`
}

type ReminderDigest struct {
	On    string               `json:"on"`
	Items []ReminderDigestItem `json:"items"`
}
