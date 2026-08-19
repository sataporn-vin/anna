package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"anna/internal/memory"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type eventApplicationStub struct {
	Application
	input               memory.EventInput
	searchInput         memory.EventSearchInput
	updateInput         memory.EventUpdateInput
	deletedID           string
	accountInput        memory.AccountInput
	paymentChannelInput memory.PaymentChannelInput
	transactionInput    memory.TransactionInput
}

func (stub *eventApplicationStub) CreateAccount(_ context.Context, input memory.AccountInput) (memory.AccountInfo, bool, error) {
	stub.accountInput = input
	return memory.AccountInfo{ID: input.ID, Name: input.Name, Kind: input.Kind, Currency: input.Currency, Active: true}, true, nil
}

func (stub *eventApplicationStub) CreatePaymentChannel(_ context.Context, input memory.PaymentChannelInput) (memory.PaymentChannelInfo, bool, error) {
	stub.paymentChannelInput = input
	return memory.PaymentChannelInfo{ID: input.ID, Name: input.Name, Active: true}, true, nil
}

func (stub *eventApplicationStub) CreateTransaction(_ context.Context, input memory.TransactionInput) (memory.WriteResult, error) {
	stub.transactionInput = input
	return memory.WriteResult{ID: "transaction-id", Created: true}, nil
}

func (stub *eventApplicationStub) CreateEvent(_ context.Context, input memory.EventInput) (memory.WriteResult, error) {
	stub.input = input
	return memory.WriteResult{ID: "event-id", Created: true}, nil
}

func (stub *eventApplicationStub) SearchEvents(_ context.Context, input memory.EventSearchInput) ([]bson.M, error) {
	stub.searchInput = input
	return []bson.M{{"title": "Company monthly dinner"}}, nil
}

func (stub *eventApplicationStub) GetEvent(_ context.Context, _ string) (bson.M, error) {
	return bson.M{"title": "Company monthly dinner"}, nil
}

func (stub *eventApplicationStub) UpdateEvent(_ context.Context, _ string, input memory.EventUpdateInput) (bson.M, error) {
	stub.updateInput = input
	return bson.M{"title": "Updated dinner"}, nil
}

func (stub *eventApplicationStub) DeleteEvent(_ context.Context, id string) error {
	stub.deletedID = id
	return nil
}

func TestProtectedEndpointRequiresBearerToken(t *testing.T) {
	handler := New(nil, "01234567890123456789012345678901", 1024, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/v1/collections", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %q", response.Header().Get("Content-Type"))
	}
}

func TestHealthIsPublic(t *testing.T) {
	handler := New(nil, "01234567890123456789012345678901", 1024, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestRecordEventCreatesManagedEvent(t *testing.T) {
	application := &eventApplicationStub{}
	handler := New(application, "01234567890123456789012345678901", 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"requestId":"d19f46f2-f760-4764-89ad-a22ae819ce6e",
		"occurredOn":"2026-08-19",
		"title":"Company monthly dinner",
		"rawText":"Company paid for dinner."
	}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if application.input.Title != "Company monthly dinner" {
		t.Fatalf("unexpected event input: %#v", application.input)
	}
}

func TestCreateAccountAndPaymentChannel(t *testing.T) {
	application := &eventApplicationStub{}
	handler := New(application, "01234567890123456789012345678901", 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tests := []struct {
		path string
		body string
	}{
		{path: "/v1/accounts", body: `{"id":"krungsri-homepro-credit-card","name":"Krungsri HomePro Credit Card","kind":"credit_card","currency":"THB"}`},
		{path: "/v1/payment-channels", body: `{"id":"truemoney-wallet","name":"TrueMoney Wallet"}`},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
		request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("%s: expected 201, got %d: %s", test.path, response.Code, response.Body.String())
		}
	}
	if application.accountInput.ID != "krungsri-homepro-credit-card" {
		t.Fatalf("unexpected account input: %#v", application.accountInput)
	}
	if application.paymentChannelInput.ID != "truemoney-wallet" {
		t.Fatalf("unexpected payment channel input: %#v", application.paymentChannelInput)
	}
}

func TestRecordTransactionAcceptsPaymentChannel(t *testing.T) {
	application := &eventApplicationStub{}
	handler := New(application, "01234567890123456789012345678901", 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := `{
		"requestId":"570b5176-95c9-4812-9a2e-d52fb9b7e11a",
		"occurredOn":"2026-08-19",
		"amountMinor":16500,
		"transactionKind":"expense",
		"accountId":"krungsri-homepro-credit-card",
		"paymentChannelId":"truemoney-wallet",
		"descriptorRaw":"7-Eleven"
	}`
	request := httptest.NewRequest(http.MethodPost, "/v1/transactions", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if application.transactionInput.PaymentChannelID != "truemoney-wallet" {
		t.Fatalf("unexpected transaction input: %#v", application.transactionInput)
	}
}

func TestSearchEventsReturnsMatchingDocuments(t *testing.T) {
	application := &eventApplicationStub{}
	handler := New(application, "01234567890123456789012345678901", 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/v1/events/search", bytes.NewReader([]byte(`{"text":"sushiro"}`)))
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if application.searchInput.Text != "sushiro" {
		t.Fatalf("unexpected search input: %#v", application.searchInput)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("Company monthly dinner")) {
		t.Fatalf("response did not contain event: %s", response.Body.String())
	}
}

func TestUpdateEventUsesValidatedMergePatch(t *testing.T) {
	application := &eventApplicationStub{}
	handler := New(application, "01234567890123456789012345678901", 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPatch, "/v1/events/66c5d3d872703e1bf75d107a", bytes.NewReader([]byte(`{"title":"Updated dinner"}`)))
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	request.Header.Set("Content-Type", "application/merge-patch+json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if string(application.updateInput["title"]) != `"Updated dinner"` {
		t.Fatalf("unexpected update input: %#v", application.updateInput)
	}
}

func TestGetAndDeleteEventUseManagedRoutes(t *testing.T) {
	application := &eventApplicationStub{}
	handler := New(application, "01234567890123456789012345678901", 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		method string
		code   int
	}{
		{method: http.MethodGet, code: http.StatusOK},
		{method: http.MethodDelete, code: http.StatusNoContent},
	} {
		request := httptest.NewRequest(test.method, "/v1/events/66c5d3d872703e1bf75d107a", nil)
		request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatalf("%s: expected %d, got %d: %s", test.method, test.code, response.Code, response.Body.String())
		}
	}
	if application.deletedID != "66c5d3d872703e1bf75d107a" {
		t.Fatalf("unexpected deleted event id: %q", application.deletedID)
	}
}
