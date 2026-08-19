package memory

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	weekdayNames          = stringSet("monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday")
	reminderPhases        = stringSet("preparation", "occurrence")
	eventTypes            = stringSet("company-event", "social-event", "meal-gathering", "appointment-completed", "travel", "celebration", "personal-milestone", "work-milestone", "health-fitness", "pet", "family", "general-memory")
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
	return name == "transactions" || name == "accounts" || name == "events" || name == "reminders" || name == "reminder_completions"
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

func ValidateReminder(input *ReminderInput, defaultTimezone string, now time.Time) error {
	if !idPattern.MatchString(input.ID) || len(input.ID) > 100 {
		return fmt.Errorf("id must be a lowercase kebab-case identifier of at most 100 characters")
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Title) > 300 {
		return fmt.Errorf("title must contain 1 to 300 characters")
	}
	if input.Timezone == "" {
		input.Timezone = defaultTimezone
	}
	location, err := time.LoadLocation(input.Timezone)
	if err != nil {
		return fmt.Errorf("timezone must be a valid IANA timezone")
	}
	if input.StartsOn == "" {
		input.StartsOn = now.In(location).Format("2006-01-02")
	}
	if _, err := parseDate(input.StartsOn, location); err != nil {
		return fmt.Errorf("startsOn must be a real date in YYYY-MM-DD format")
	}
	if len(input.Weekdays) == 0 || len(input.Weekdays) > 7 {
		return fmt.Errorf("weekdays must contain between 1 and 7 values")
	}
	seen := make(map[string]bool, len(input.Weekdays))
	for _, weekday := range input.Weekdays {
		if !weekdayNames[weekday] {
			return fmt.Errorf("weekday %q is not supported", weekday)
		}
		if seen[weekday] {
			return fmt.Errorf("weekday %q is repeated", weekday)
		}
		seen[weekday] = true
	}
	if input.Preparation != nil {
		input.Preparation.Title = strings.TrimSpace(input.Preparation.Title)
		if input.Preparation.Title == "" || len(input.Preparation.Title) > 300 {
			return fmt.Errorf("preparation.title must contain 1 to 300 characters")
		}
		if input.Preparation.LeadDays < 1 || input.Preparation.LeadDays > 30 {
			return fmt.Errorf("preparation.leadDays must be between 1 and 30")
		}
	}
	return nil
}

func ValidateReminderCompletion(input ReminderCompletionInput) error {
	if !idPattern.MatchString(input.ReminderID) || len(input.ReminderID) > 100 {
		return fmt.Errorf("reminderId must be a lowercase kebab-case identifier")
	}
	if _, err := parseDate(input.OccurrenceOn, time.UTC); err != nil {
		return fmt.Errorf("occurrenceOn must be a real date in YYYY-MM-DD format")
	}
	if !reminderPhases[input.Phase] {
		return fmt.Errorf("phase must be preparation or occurrence")
	}
	return nil
}

func parseDate(value string, location *time.Location) (time.Time, error) {
	date, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil || date.Format("2006-01-02") != value {
		return time.Time{}, fmt.Errorf("invalid date")
	}
	return date, nil
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

func ValidateEvent(input *EventInput, defaultTimezone string, now time.Time) error {
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
	occurredOn, err := parseDate(input.OccurredOn, location)
	if err != nil {
		return fmt.Errorf("occurredOn must be a real date in YYYY-MM-DD format")
	}
	today, _ := parseDate(now.In(location).Format("2006-01-02"), location)
	if occurredOn.After(today) {
		return fmt.Errorf("occurredOn must not be in the future")
	}
	if input.OccurredAt != nil && input.OccurredAt.In(location).Format("2006-01-02") != input.OccurredOn {
		return fmt.Errorf("occurredAt does not occur on occurredOn in timezone")
	}
	if input.OccurredAt != nil && input.OccurredAt.After(now) {
		return fmt.Errorf("occurredAt must not be in the future")
	}
	input.Title = normalizeDisplay(input.Title)
	if runeLength(input.Title) < 1 || runeLength(input.Title) > 300 {
		return fmt.Errorf("title must contain 1 to 300 characters")
	}
	if input.EventType == "" {
		input.EventType = "general-memory"
	}
	if !eventTypes[input.EventType] {
		return fmt.Errorf("eventType is not supported")
	}
	if input.Location != nil {
		normalized := normalizeDisplay(*input.Location)
		if runeLength(normalized) < 1 || runeLength(normalized) > 500 {
			return fmt.Errorf("location must contain 1 to 500 characters")
		}
		input.Location = &normalized
	}
	if input.Description != nil {
		normalized := strings.TrimSpace(norm.NFKC.String(*input.Description))
		if runeLength(normalized) > 5000 {
			return fmt.Errorf("description must not exceed 5000 characters")
		}
		input.Description = &normalized
	}
	if strings.TrimSpace(input.RawText) == "" || runeLength(input.RawText) > 5000 {
		return fmt.Errorf("rawText must contain 1 to 5000 characters")
	}
	if err := normalizeEventPeople(input); err != nil {
		return err
	}
	if err := normalizeEventTags(input); err != nil {
		return err
	}
	searchValues := []string{input.Title}
	if input.Location != nil {
		searchValues = append(searchValues, *input.Location)
	}
	searchValues = append(searchValues, input.People...)
	searchValues = append(searchValues, input.Tags...)
	searchTokens := eventSearchTokens(searchValues...)
	if len(searchTokens) > 100 {
		return fmt.Errorf("event content must not exceed 100 searchable tokens")
	}
	for _, token := range searchTokens {
		if runeLength(token) > 500 {
			return fmt.Errorf("event search tokens must not exceed 500 characters")
		}
	}
	if err := validateFinancialContext(input.FinancialContext); err != nil {
		return err
	}
	if len(input.RelatedTransactionIDs) > 20 {
		return fmt.Errorf("relatedTransactionIds must not exceed 20 values")
	}
	seenTransactions := make(map[string]bool, len(input.RelatedTransactionIDs))
	for _, id := range input.RelatedTransactionIDs {
		if _, err := bson.ObjectIDFromHex(id); err != nil {
			return fmt.Errorf("relatedTransactionIds contains an invalid object identifier")
		}
		if seenTransactions[id] {
			return fmt.Errorf("relatedTransactionIds contains a duplicate")
		}
		seenTransactions[id] = true
	}
	if input.FinancialContext != nil && input.FinancialContext.PersonalPaymentMinor != nil && *input.FinancialContext.PersonalPaymentMinor > 0 && len(input.RelatedTransactionIDs) == 0 {
		return fmt.Errorf("relatedTransactionIds is required when personalPaymentMinor is positive")
	}
	return validateEventAttributes(input.Attributes)
}

func ValidateEventSearch(input *EventSearchInput, maxResults int64) error {
	if input.OccurredFrom != "" {
		if _, err := parseDate(input.OccurredFrom, time.UTC); err != nil {
			return fmt.Errorf("occurredFrom must be a real date in YYYY-MM-DD format")
		}
	}
	if input.OccurredTo != "" {
		if _, err := parseDate(input.OccurredTo, time.UTC); err != nil {
			return fmt.Errorf("occurredTo must be a real date in YYYY-MM-DD format")
		}
	}
	if input.OccurredFrom != "" && input.OccurredTo != "" && input.OccurredFrom > input.OccurredTo {
		return fmt.Errorf("occurredFrom must not follow occurredTo")
	}
	if input.CreatedFrom != nil && input.CreatedTo != nil && !input.CreatedFrom.Before(*input.CreatedTo) {
		return fmt.Errorf("createdFrom must be before createdTo")
	}
	seenTypes := make(map[string]bool, len(input.EventTypes))
	for _, eventType := range input.EventTypes {
		if !eventTypes[eventType] {
			return fmt.Errorf("eventTypes contains an unsupported value")
		}
		if seenTypes[eventType] {
			return fmt.Errorf("eventTypes contains a duplicate")
		}
		seenTypes[eventType] = true
	}
	if input.Location != "" {
		input.Location = normalizeSearchValue(input.Location)
		if runeLength(input.Location) > 500 {
			return fmt.Errorf("location must not exceed 500 characters")
		}
	}
	if len(input.People) > 50 {
		return fmt.Errorf("people must not exceed 50 values")
	}
	for index, person := range input.People {
		input.People[index] = normalizeSearchValue(person)
		if input.People[index] == "" || runeLength(input.People[index]) > 200 {
			return fmt.Errorf("people contains an invalid value")
		}
	}
	if len(input.Tags) > 20 {
		return fmt.Errorf("tags must not exceed 20 values")
	}
	for index, tag := range input.Tags {
		input.Tags[index] = normalizeSearchValue(tag)
		if input.Tags[index] == "" || runeLength(input.Tags[index]) > 50 {
			return fmt.Errorf("tags contains an invalid value")
		}
	}
	input.SearchTokens = eventSearchTokens(input.Text)
	if input.Text != "" && len(input.SearchTokens) == 0 {
		return fmt.Errorf("text must contain at least one searchable token")
	}
	if len(input.SearchTokens) > 20 {
		return fmt.Errorf("text must not exceed 20 searchable tokens")
	}
	for _, token := range input.SearchTokens {
		if runeLength(token) > 500 {
			return fmt.Errorf("text tokens must not exceed 500 characters")
		}
	}
	if input.Sort == "" {
		input.Sort = "occurred_desc"
	}
	if input.Sort != "occurred_desc" && input.Sort != "occurred_asc" && input.Sort != "created_desc" {
		return fmt.Errorf("sort is not supported")
	}
	if input.Limit == 0 {
		input.Limit = 50
	}
	limit := min(int64(100), maxResults)
	if input.Limit < 1 || input.Limit > limit {
		return fmt.Errorf("limit must be between 1 and %d", limit)
	}
	if input.OccurredFrom == "" && input.OccurredTo == "" && input.CreatedFrom == nil && input.CreatedTo == nil && len(input.EventTypes) == 0 && input.Location == "" && len(input.People) == 0 && len(input.Tags) == 0 && len(input.SearchTokens) == 0 {
		return fmt.Errorf("at least one search filter is required")
	}
	return nil
}

func normalizeEventPeople(input *EventInput) error {
	if len(input.People) > 50 {
		return fmt.Errorf("people must not exceed 50 values")
	}
	seen := make(map[string]bool, len(input.People))
	people := make([]string, 0, len(input.People))
	for _, value := range input.People {
		display := normalizeDisplay(value)
		if runeLength(display) < 1 || runeLength(display) > 200 {
			return fmt.Errorf("people contains a value outside 1 to 200 characters")
		}
		key := normalizeSearchValue(display)
		if !seen[key] {
			seen[key] = true
			people = append(people, display)
		}
	}
	input.People = people
	return nil
}

func normalizeEventTags(input *EventInput) error {
	if len(input.Tags) > 20 {
		return fmt.Errorf("tags must not exceed 20 values")
	}
	seen := make(map[string]bool, len(input.Tags))
	tags := make([]string, 0, len(input.Tags))
	for _, value := range input.Tags {
		tag := normalizeSearchValue(value)
		if runeLength(tag) < 1 || runeLength(tag) > 50 {
			return fmt.Errorf("tags contains a value outside 1 to 50 characters")
		}
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	input.Tags = tags
	return nil
}

func validateFinancialContext(context *EventFinancialContextInput) error {
	if context == nil {
		return nil
	}
	if !currencyPattern.MatchString(context.Currency) {
		return fmt.Errorf("financialContext.currency must be a three-letter uppercase code")
	}
	amounts := []*int64{context.TotalValueMinor, context.AllowanceMinor, context.CoveredByOthersMinor, context.PersonalPaymentMinor}
	hasAmount := false
	for _, amount := range amounts {
		if amount == nil {
			continue
		}
		hasAmount = true
		if *amount < 0 {
			return fmt.Errorf("financialContext amounts must not be negative")
		}
	}
	if !hasAmount {
		return fmt.Errorf("financialContext must contain at least one amount")
	}
	return nil
}

func validateEventAttributes(attributes map[string]any) error {
	if len(attributes) > 32 {
		return fmt.Errorf("attributes must not exceed 32 fields")
	}
	for key, value := range attributes {
		if strings.TrimSpace(key) != key || key == "" || runeLength(key) > 64 || strings.HasPrefix(key, "$") || strings.Contains(key, ".") {
			return fmt.Errorf("attribute key %q is invalid", key)
		}
		if err := validateEventAttributeValue(value); err != nil {
			return fmt.Errorf("attribute %q: %w", key, err)
		}
	}
	encoded, err := json.Marshal(attributes)
	if err != nil {
		return fmt.Errorf("attributes must contain JSON-compatible values")
	}
	if len(encoded) > 16*1024 {
		return fmt.Errorf("attributes must not exceed 16384 encoded bytes")
	}
	return nil
}

func validateEventAttributeValue(value any) error {
	switch typed := value.(type) {
	case nil, bool, int, int32, int64, float32, float64, json.Number:
		return nil
	case string:
		if runeLength(typed) > 1000 {
			return fmt.Errorf("string values must not exceed 1000 characters")
		}
		return nil
	case []any:
		if len(typed) > 20 {
			return fmt.Errorf("arrays must not exceed 20 values")
		}
		for _, item := range typed {
			switch item.(type) {
			case map[string]any, []any:
				return fmt.Errorf("nested objects and arrays are not allowed")
			}
			if err := validateEventAttributeValue(item); err != nil {
				return err
			}
		}
		return nil
	case []string:
		if len(typed) > 20 {
			return fmt.Errorf("arrays must not exceed 20 values")
		}
		for _, item := range typed {
			if err := validateEventAttributeValue(item); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("values must be JSON scalars or scalar arrays")
	}
}

func normalizeDisplay(value string) string {
	return strings.Join(strings.Fields(norm.NFKC.String(value)), " ")
}

func normalizeSearchValue(value string) string {
	return strings.ToLower(normalizeDisplay(value))
}

func eventSearchTokens(values ...string) []string {
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, value := range values {
		for _, token := range strings.FieldsFunc(normalizeSearchValue(value), func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
		}) {
			if token != "" && !seen[token] {
				seen[token] = true
				result = append(result, token)
			}
		}
	}
	return result
}

func runeLength(value string) int { return utf8.RuneCountInString(value) }

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
