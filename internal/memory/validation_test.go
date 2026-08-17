package memory

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestValidateTransactionSplitDate(t *testing.T) {
	descriptor := "Fuji"
	validInstant := time.Date(2026, time.August, 15, 1, 30, 0, 0, time.UTC)
	input := TransactionInput{
		RequestID:       "22f2409c-6b93-489e-96a3-b7433107fb2f",
		OccurredOn:      "2026-08-15",
		OccurredAt:      &validInstant,
		Timezone:        "Asia/Bangkok",
		AmountMinor:     48500,
		Currency:        "THB",
		TransactionKind: "expense",
		AccountID:       "kbank-visa",
		DescriptorRaw:   &descriptor,
	}
	if err := ValidateTransaction(&input, "Asia/Bangkok"); err != nil {
		t.Fatalf("expected transaction to be valid: %v", err)
	}

	wrongDate := input
	wrongDate.OccurredOn = "2026-08-14"
	if err := ValidateTransaction(&wrongDate, "Asia/Bangkok"); err == nil || !strings.Contains(err.Error(), "does not occur") {
		t.Fatalf("expected local-date mismatch, got %v", err)
	}
}

func TestValidateTransactionAllowsDateOnly(t *testing.T) {
	descriptor := "BTS"
	input := TransactionInput{
		RequestID:       "bc1a80e5-9e68-46e1-9cb1-6205d404ca1c",
		OccurredOn:      "2026-07-11",
		AmountMinor:     2500,
		TransactionKind: "expense",
		AccountID:       "cash",
		DescriptorRaw:   &descriptor,
	}
	if err := ValidateTransaction(&input, "Asia/Bangkok"); err != nil {
		t.Fatalf("expected date-only transaction to be valid: %v", err)
	}
	if input.Timezone != "Asia/Bangkok" || input.Currency != "THB" {
		t.Fatalf("expected defaults to be applied, got timezone=%q currency=%q", input.Timezone, input.Currency)
	}
}

func TestMongoSafetyValidation(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "where filter", err: ValidateFilter(bson.D{{Key: "$where", Value: "true"}}, true)},
		{name: "regex value", err: ValidateFilter(bson.D{{Key: "name", Value: bson.Regex{Pattern: ".*"}}}, true)},
		{name: "server field update", err: ValidateUpdate(bson.D{{Key: "$set", Value: bson.D{{Key: "createdAt", Value: time.Now()}}}})},
		{name: "out stage", err: ValidatePipeline([]bson.D{{{Key: "$out", Value: "other"}}}, 10)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNormalizeDescriptor(t *testing.T) {
	raw := "  ＦＵＪＩ   Restaurant  "
	normalized := NormalizeDescriptor(&raw)
	if normalized == nil || *normalized != "fuji restaurant" {
		t.Fatalf("unexpected normalization: %v", normalized)
	}
}

func TestValidateReminder(t *testing.T) {
	input := ReminderInput{
		ID:          "company-uniform",
		Title:       "Wear the company uniform",
		Weekdays:    []string{"monday", "friday"},
		Preparation: &ReminderPreparationInput{Title: "Wash the company uniform", LeadDays: 2},
	}
	if err := ValidateReminder(&input, "Asia/Bangkok", time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("expected reminder to be valid: %v", err)
	}
	if input.Timezone != "Asia/Bangkok" || input.StartsOn != "2026-08-17" {
		t.Fatalf("expected defaults, got timezone=%q startsOn=%q", input.Timezone, input.StartsOn)
	}

	duplicateWeekday := input
	duplicateWeekday.Weekdays = []string{"monday", "monday"}
	if err := ValidateReminder(&duplicateWeekday, "Asia/Bangkok", time.Now()); err == nil {
		t.Fatal("expected duplicate weekday to be rejected")
	}
}
