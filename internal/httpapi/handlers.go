package httpapi

import (
	"encoding/json"
	"net/http"

	"anna/internal/memory"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type collectionRequest struct {
	Name string `json:"name"`
}

type findRequest struct {
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter"`
	Projection json.RawMessage `json:"projection"`
	Sort       json.RawMessage `json:"sort"`
	Limit      int64           `json:"limit"`
}

type insertRequest struct {
	Collection string          `json:"collection"`
	Document   json.RawMessage `json:"document"`
}

type updateRequest struct {
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter"`
	Update     json.RawMessage `json:"update"`
}

type deleteRequest struct {
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter"`
}

type aggregateRequest struct {
	Collection string            `json:"collection"`
	Pipeline   []json.RawMessage `json:"pipeline"`
}

func (server *Server) listCollections(writer http.ResponseWriter, request *http.Request) {
	collections, err := server.application.ListCollections(request.Context())
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": collections, "meta": map[string]int{"count": len(collections)}})
}

func (server *Server) createCollection(writer http.ResponseWriter, request *http.Request) {
	var input collectionRequest
	if err := decodeJSON(writer, request, server.maxBody, &input); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	collection, created, err := server.application.CreateCollection(request.Context(), input.Name)
	if err != nil {
		server.handleError(writer, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"data": collection, "created": created})
}

func (server *Server) createAccount(writer http.ResponseWriter, request *http.Request) {
	var input memory.AccountInput
	if err := decodeJSON(writer, request, server.maxBody, &input); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	account, created, err := server.application.CreateAccount(request.Context(), input)
	if err != nil {
		server.handleError(writer, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"data": account, "created": created})
}

func (server *Server) createTransaction(writer http.ResponseWriter, request *http.Request) {
	var input memory.TransactionInput
	if err := decodeJSON(writer, request, server.maxBody, &input); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	result, err := server.application.CreateTransaction(request.Context(), input)
	if err != nil {
		server.handleError(writer, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeWriteResult(writer, status, result)
}

func (server *Server) find(writer http.ResponseWriter, request *http.Request) {
	input, err := server.decodeFind(writer, request)
	if err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	documents, err := server.application.Find(request.Context(), input)
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeDocuments(writer, http.StatusOK, documents)
}

func (server *Server) findOne(writer http.ResponseWriter, request *http.Request) {
	input, err := server.decodeFind(writer, request)
	if err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	document, err := server.application.FindOne(request.Context(), input)
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeDocument(writer, http.StatusOK, document)
}

func (server *Server) insertOne(writer http.ResponseWriter, request *http.Request) {
	var body insertRequest
	if err := decodeJSON(writer, request, server.maxBody, &body); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	document, err := decodeExtJSONDocument(body.Document, false)
	if err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	result, err := server.application.InsertOne(request.Context(), body.Collection, document)
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeWriteResult(writer, http.StatusCreated, result)
}

func (server *Server) updateOne(writer http.ResponseWriter, request *http.Request) {
	var body updateRequest
	if err := decodeJSON(writer, request, server.maxBody, &body); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	filter, err := decodeExtJSONDocument(body.Filter, true)
	if err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	update, err := decodeExtJSONDocument(body.Update, false)
	if err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	result, err := server.application.UpdateOne(request.Context(), memory.UpdateInput{Collection: body.Collection, Filter: filter, Update: update})
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeExtJSON(writer, http.StatusOK, bson.M{"data": bson.M{
		"matchedCount": result.MatchedCount, "modifiedCount": result.ModifiedCount,
	}})
}

func (server *Server) deleteOne(writer http.ResponseWriter, request *http.Request) {
	var body deleteRequest
	if err := decodeJSON(writer, request, server.maxBody, &body); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	filter, err := decodeExtJSONDocument(body.Filter, true)
	if err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	result, err := server.application.DeleteOne(request.Context(), memory.DeleteInput{Collection: body.Collection, Filter: filter})
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeExtJSON(writer, http.StatusOK, bson.M{"data": bson.M{"deletedCount": result.DeletedCount}})
}

func (server *Server) aggregate(writer http.ResponseWriter, request *http.Request) {
	var body aggregateRequest
	if err := decodeJSON(writer, request, server.maxBody, &body); err != nil {
		server.handleError(writer, memory.Invalid(err))
		return
	}
	pipeline := make([]bson.D, len(body.Pipeline))
	for index, rawStage := range body.Pipeline {
		stage, err := decodeExtJSONDocument(rawStage, false)
		if err != nil {
			server.handleError(writer, memory.Invalid(err))
			return
		}
		pipeline[index] = stage
	}
	documents, err := server.application.Aggregate(request.Context(), memory.AggregateInput{Collection: body.Collection, Pipeline: pipeline})
	if err != nil {
		server.handleError(writer, err)
		return
	}
	writeDocuments(writer, http.StatusOK, documents)
}

func (server *Server) decodeFind(writer http.ResponseWriter, request *http.Request) (memory.FindInput, error) {
	var body findRequest
	if err := decodeJSON(writer, request, server.maxBody, &body); err != nil {
		return memory.FindInput{}, err
	}
	filter, err := decodeExtJSONDocument(body.Filter, true)
	if err != nil {
		return memory.FindInput{}, err
	}
	projection, err := decodeExtJSONDocument(body.Projection, true)
	if err != nil {
		return memory.FindInput{}, err
	}
	sort, err := decodeExtJSONDocument(body.Sort, true)
	if err != nil {
		return memory.FindInput{}, err
	}
	return memory.FindInput{Collection: body.Collection, Filter: filter, Projection: projection, Sort: sort, Limit: body.Limit}, nil
}
