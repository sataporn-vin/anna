package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"anna/internal/memory"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Application interface {
	Ping(context.Context) error
	ListCollections(context.Context) ([]memory.CollectionInfo, error)
	CreateCollection(context.Context, string) (memory.CollectionInfo, bool, error)
	Find(context.Context, memory.FindInput) ([]bson.M, error)
	FindOne(context.Context, memory.FindInput) (bson.M, error)
	InsertOne(context.Context, string, bson.D) (memory.WriteResult, error)
	UpdateOne(context.Context, memory.UpdateInput) (memory.WriteResult, error)
	DeleteOne(context.Context, memory.DeleteInput) (memory.WriteResult, error)
	Aggregate(context.Context, memory.AggregateInput) ([]bson.M, error)
	CreateAccount(context.Context, memory.AccountInput) (memory.AccountInfo, bool, error)
	CreatePaymentChannel(context.Context, memory.PaymentChannelInput) (memory.PaymentChannelInfo, bool, error)
	CreateTransaction(context.Context, memory.TransactionInput) (memory.WriteResult, error)
	CreateEvent(context.Context, memory.EventInput) (memory.WriteResult, error)
	GetEvent(context.Context, string) (bson.M, error)
	SearchEvents(context.Context, memory.EventSearchInput) ([]bson.M, error)
	UpdateEvent(context.Context, string, memory.EventUpdateInput) (bson.M, error)
	DeleteEvent(context.Context, string) error
	CreateReminder(context.Context, memory.ReminderInput) (memory.ReminderInfo, bool, error)
	ReminderDigest(context.Context, string) (memory.ReminderDigest, error)
	CompleteReminder(context.Context, memory.ReminderCompletionInput) (memory.WriteResult, error)
}

type Server struct {
	application Application
	tokenDigest [sha256.Size]byte
	maxBody     int64
	logger      *slog.Logger
}

func New(application Application, token string, maxBody int64, logger *slog.Logger) http.Handler {
	server := &Server{
		application: application,
		tokenDigest: sha256.Sum256([]byte(token)),
		maxBody:     maxBody,
		logger:      logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", server.live)
	mux.HandleFunc("GET /health/ready", server.ready)
	mux.Handle("GET /v1/collections", server.authenticate(http.HandlerFunc(server.listCollections)))
	mux.Handle("POST /v1/collections", server.authenticate(http.HandlerFunc(server.createCollection)))
	mux.Handle("POST /v1/accounts", server.authenticate(http.HandlerFunc(server.createAccount)))
	mux.Handle("POST /v1/payment-channels", server.authenticate(http.HandlerFunc(server.createPaymentChannel)))
	mux.Handle("POST /v1/transactions", server.authenticate(http.HandlerFunc(server.createTransaction)))
	mux.Handle("POST /v1/events", server.authenticate(http.HandlerFunc(server.createEvent)))
	mux.Handle("GET /v1/events/{id}", server.authenticate(http.HandlerFunc(server.getEvent)))
	mux.Handle("POST /v1/events/search", server.authenticate(http.HandlerFunc(server.searchEvents)))
	mux.Handle("PATCH /v1/events/{id}", server.authenticate(http.HandlerFunc(server.updateEvent)))
	mux.Handle("DELETE /v1/events/{id}", server.authenticate(http.HandlerFunc(server.deleteEvent)))
	mux.Handle("POST /v1/reminders", server.authenticate(http.HandlerFunc(server.createReminder)))
	mux.Handle("GET /v1/reminders/digest", server.authenticate(http.HandlerFunc(server.reminderDigest)))
	mux.Handle("POST /v1/reminders/completions", server.authenticate(http.HandlerFunc(server.completeReminder)))
	mux.Handle("POST /v1/mongo/find", server.authenticate(http.HandlerFunc(server.find)))
	mux.Handle("POST /v1/mongo/find-one", server.authenticate(http.HandlerFunc(server.findOne)))
	mux.Handle("POST /v1/mongo/insert-one", server.authenticate(http.HandlerFunc(server.insertOne)))
	mux.Handle("POST /v1/mongo/update-one", server.authenticate(http.HandlerFunc(server.updateOne)))
	mux.Handle("POST /v1/mongo/delete-one", server.authenticate(http.HandlerFunc(server.deleteOne)))
	mux.Handle("POST /v1/mongo/aggregate", server.authenticate(http.HandlerFunc(server.aggregate)))

	return server.logRequests(mux)
}

func (server *Server) live(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (server *Server) ready(writer http.ResponseWriter, request *http.Request) {
	if err := server.application.Ping(request.Context()); err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (server *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "a bearer token is required")
			return
		}
		provided := sha256.Sum256([]byte(strings.TrimPrefix(authorization, "Bearer ")))
		if subtle.ConstantTimeCompare(provided[:], server.tokenDigest[:]) != 1 {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "the bearer token is invalid")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) handleError(writer http.ResponseWriter, err error) {
	var validationError memory.ValidationError
	var maxBytesError *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytesError):
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
	case errors.As(err, &validationError):
		writeError(writer, http.StatusBadRequest, "invalid_request", validationError.Error())
	case errors.Is(err, memory.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "the requested resource was not found")
	case errors.Is(err, memory.ErrManagedCollection):
		writeError(writer, http.StatusForbidden, "managed_collection", "generic writes are not allowed for this collection")
	case errors.Is(err, memory.ErrInactiveAccount):
		writeError(writer, http.StatusUnprocessableEntity, "inactive_account", "account does not exist or is inactive")
	case errors.Is(err, memory.ErrInactivePaymentChannel):
		writeError(writer, http.StatusUnprocessableEntity, "inactive_payment_channel", "payment channel does not exist or is inactive")
	case errors.Is(err, memory.ErrIdempotencyConflict):
		writeError(writer, http.StatusConflict, "idempotency_conflict", "requestId was already used for different content")
	case errors.Is(err, memory.ErrAccountExists):
		writeError(writer, http.StatusConflict, "account_exists", "an account with this id already exists")
	case errors.Is(err, memory.ErrPaymentChannelExists):
		writeError(writer, http.StatusConflict, "payment_channel_exists", "a payment channel with this id already exists")
	case errors.Is(err, memory.ErrReminderExists):
		writeError(writer, http.StatusConflict, "reminder_exists", "a reminder with this id already exists")
	case errors.Is(err, memory.ErrResultLimit):
		writeError(writer, http.StatusUnprocessableEntity, "result_too_large", "the result exceeds a configured limit")
	case errors.Is(err, memory.ErrCollectionLimit):
		writeError(writer, http.StatusConflict, "collection_limit", "the collection limit has been reached")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(writer, http.StatusGatewayTimeout, "operation_timeout", "the database operation timed out")
	default:
		server.logger.Error("request failed", "error", err)
		writeError(writer, http.StatusInternalServerError, "internal_error", "the request could not be completed")
	}
}
