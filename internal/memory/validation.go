package memory

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/unicode/norm"
)

var (
	collectionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	idPattern             = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	currencyPattern       = regexp.MustCompile(`^[A-Z]{3}$`)
	filterOperators       = stringSet("$and", "$or", "$nor", "$eq", "$ne", "$gt", "$gte", "$lt", "$lte", "$in", "$nin", "$exists")
	updateOperators       = stringSet("$set", "$unset", "$inc")
	stageOperators        = stringSet("$match", "$project", "$sort", "$limit", "$skip", "$group", "$count", "$unwind")
	expressionOperators   = stringSet("$sum", "$avg", "$min", "$max", "$first", "$last", "$dateToString", "$literal")
	transactionKinds      = stringSet("expense", "income", "refund", "credit_card_payment", "transfer")
	accountKinds          = stringSet("bank_account", "credit_card", "cash", "wallet", "other")
	serverFields          = stringSet("_id", "createdAt", "updatedAt")
)

func ValidateCollectionName(name string) error {
	if !collectionNamePattern.MatchString(name) {
		return fmt.Errorf("collection name must match %s", collectionNamePattern)
	}
	if IsReservedCollection(name) {
		return fmt.Errorf("collection name %q is reserved", name)
	}
	return nil
}

func IsReservedCollection(name string) bool {
	return strings.HasPrefix(name, "system.") || strings.HasPrefix(name, "_memory_")
}

func IsManagedCollection(name string) bool {
	return name == "transactions" || name == "accounts"
}

func ValidateFilter(document bson.D, allowEmpty bool) error {
	if !allowEmpty && len(document) == 0 {
		return fmt.Errorf("filter must contain at least one field")
	}
	return validateDocument(document, filterOperators)
}

func ValidateProjection(document bson.D) error {
	for _, element := range document {
		if err := validateFieldName(element.Key); err != nil {
			return err
		}
		value, ok := integerValue(element.Value)
		if !ok || (value != 0 && value != 1) {
			return fmt.Errorf("projection field %q must be 0 or 1", element.Key)
		}
	}
	return nil
}

func ValidateSort(document bson.D) error {
	for _, element := range document {
		if err := validateFieldName(element.Key); err != nil {
			return err
		}
		value, ok := integerValue(element.Value)
		if !ok || (value != -1 && value != 1) {
			return fmt.Errorf("sort field %q must be -1 or 1", element.Key)
		}
	}
	return nil
}

func ValidateInsert(document bson.D) error {
	if len(document) == 0 {
		return fmt.Errorf("document must contain at least one field")
	}
	for _, element := range document {
		if serverFields[element.Key] {
			return fmt.Errorf("field %q is owned by the server", element.Key)
		}
	}
	return validateDocument(document, nil)
}

func ValidateUpdate(update bson.D) error {
	if len(update) == 0 {
		return fmt.Errorf("update must contain at least one operator")
	}
	for _, operation := range update {
		if !updateOperators[operation.Key] {
			return fmt.Errorf("update operator %q is not allowed", operation.Key)
		}
		fields, ok := asDocument(operation.Value)
		if !ok || len(fields) == 0 {
			return fmt.Errorf("update operator %q must contain fields", operation.Key)
		}
		for _, field := range fields {
			root := strings.Split(field.Key, ".")[0]
			if serverFields[root] {
				return fmt.Errorf("field %q is owned by the server", field.Key)
			}
			if err := validateFieldName(field.Key); err != nil {
				return err
			}
			if err := rejectUnsafeValue(field.Value); err != nil {
				return fmt.Errorf("field %q: %w", field.Key, err)
			}
			if operation.Key == "$inc" && !isNumber(field.Value) {
				return fmt.Errorf("$inc field %q must be numeric", field.Key)
			}
		}
	}
	return nil
}

func ValidatePipeline(pipeline []bson.D, maxStages int) error {
	if len(pipeline) == 0 {
		return fmt.Errorf("pipeline must contain at least one stage")
	}
	if len(pipeline) > maxStages {
		return fmt.Errorf("pipeline exceeds the maximum of %d stages", maxStages)
	}
	allowedNested := mergeSets(filterOperators, expressionOperators)
	for index, stage := range pipeline {
		if len(stage) != 1 || !stageOperators[stage[0].Key] {
			return fmt.Errorf("pipeline stage %d is not allowed", index)
		}
		if err := validateValue(stage[0].Value, allowedNested); err != nil {
			return fmt.Errorf("pipeline stage %d: %w", index, err)
		}
	}
	return nil
}

func ValidateAccount(input AccountInput) error {
	if !idPattern.MatchString(input.ID) || len(input.ID) > 100 {
		return fmt.Errorf("id must be a lowercase kebab-case identifier of at most 100 characters")
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 200 {
		return fmt.Errorf("name must contain 1 to 200 characters")
	}
	if !accountKinds[input.Kind] {
		return fmt.Errorf("kind is not supported")
	}
	if !currencyPattern.MatchString(input.Currency) {
		return fmt.Errorf("currency must be a three-letter uppercase code")
	}
	return nil
}

func ValidateTransaction(input *TransactionInput, defaultTimezone string) error {
	if _, err := uuid.Parse(input.RequestID); err != nil {
		return fmt.Errorf("requestId must be a UUID")
	}
	if input.Timezone == "" {
		input.Timezone = defaultTimezone
	}
	location, err := time.LoadLocation(input.Timezone)
	if err != nil {
		return fmt.Errorf("timezone must be a valid IANA timezone")
	}
	date, err := time.Parse("2006-01-02", input.OccurredOn)
	if err != nil || date.Format("2006-01-02") != input.OccurredOn {
		return fmt.Errorf("occurredOn must be a real date in YYYY-MM-DD format")
	}
	if input.OccurredAt != nil && input.OccurredAt.In(location).Format("2006-01-02") != input.OccurredOn {
		return fmt.Errorf("occurredAt does not occur on occurredOn in timezone")
	}
	if input.AmountMinor <= 0 {
		return fmt.Errorf("amountMinor must be positive")
	}
	if input.Currency == "" {
		input.Currency = "THB"
	}
	if !currencyPattern.MatchString(input.Currency) {
		return fmt.Errorf("currency must be a three-letter uppercase code")
	}
	if !transactionKinds[input.TransactionKind] {
		return fmt.Errorf("transactionKind is not supported")
	}
	if !idPattern.MatchString(input.AccountID) || len(input.AccountID) > 100 {
		return fmt.Errorf("accountId must be a lowercase kebab-case identifier")
	}
	if input.DescriptorRaw == nil && input.TransactionKind != "income" {
		return fmt.Errorf("descriptorRaw is required unless transactionKind is income")
	}
	if input.DescriptorRaw != nil && len(*input.DescriptorRaw) > 1000 {
		return fmt.Errorf("descriptorRaw must not exceed 1000 characters")
	}
	if input.MerchantName != nil && len(*input.MerchantName) > 300 {
		return fmt.Errorf("merchantName must not exceed 300 characters")
	}
	if input.Note != nil && len(*input.Note) > 5000 {
		return fmt.Errorf("note must not exceed 5000 characters")
	}
	if input.RawText != nil && len(*input.RawText) > 5000 {
		return fmt.Errorf("rawText must not exceed 5000 characters")
	}
	if len(input.CategoryPath) > 8 {
		return fmt.Errorf("categoryPath must not exceed 8 segments")
	}
	for _, segment := range input.CategoryPath {
		if strings.TrimSpace(segment) == "" || segment == "." || len(segment) > 100 {
			return fmt.Errorf("categoryPath contains an invalid segment")
		}
	}
	return nil
}

func NormalizeDescriptor(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(*value)), " "))
	return &normalized
}

func validateDocument(document bson.D, allowedOperators map[string]bool) error {
	for _, element := range document {
		if strings.HasPrefix(element.Key, "$") {
			if allowedOperators == nil || !allowedOperators[element.Key] {
				return fmt.Errorf("operator %q is not allowed", element.Key)
			}
		} else if err := validateFieldName(element.Key); err != nil {
			return err
		}
		if err := validateValue(element.Value, allowedOperators); err != nil {
			return fmt.Errorf("field %q: %w", element.Key, err)
		}
	}
	return nil
}

func validateValue(value any, allowedOperators map[string]bool) error {
	if err := rejectUnsafeValue(value); err != nil {
		return err
	}
	if document, ok := asDocument(value); ok {
		return validateDocument(document, allowedOperators)
	}
	if values, ok := asArray(value); ok {
		for _, item := range values {
			if err := validateValue(item, allowedOperators); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectUnsafeValue(value any) error {
	switch value.(type) {
	case bson.Regex, *bson.Regex, bson.JavaScript, bson.CodeWithScope:
		return fmt.Errorf("regular expressions and JavaScript values are not allowed")
	}
	return nil
}

func validateFieldName(name string) error {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "$") {
		return fmt.Errorf("field name %q is invalid", name)
	}
	return nil
}

func asDocument(value any) (bson.D, bool) {
	switch typed := value.(type) {
	case bson.D:
		return typed, true
	case bson.M:
		document := make(bson.D, 0, len(typed))
		for key, value := range typed {
			document = append(document, bson.E{Key: key, Value: value})
		}
		return document, true
	default:
		return nil, false
	}
}

func asArray(value any) ([]any, bool) {
	switch typed := value.(type) {
	case bson.A:
		return []any(typed), true
	case []any:
		return typed, true
	default:
		return nil, false
	}
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func isNumber(value any) bool {
	kind := reflect.TypeOf(value)
	if kind == nil {
		return false
	}
	switch kind.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func mergeSets(sets ...map[string]bool) map[string]bool {
	merged := map[string]bool{}
	for _, set := range sets {
		for key := range set {
			merged[key] = true
		}
	}
	return merged
}
