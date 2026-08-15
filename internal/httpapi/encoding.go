package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"anna/internal/memory"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func decodeJSON(writer http.ResponseWriter, request *http.Request, maxBody int64, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain one JSON value")
		}
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func decodeExtJSONDocument(raw json.RawMessage, defaultEmpty bool) (bson.D, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		if defaultEmpty {
			return bson.D{}, nil
		}
		return nil, fmt.Errorf("document is required")
	}
	var document bson.D
	if err := bson.UnmarshalExtJSON(raw, false, &document); err != nil {
		return nil, fmt.Errorf("invalid MongoDB Extended JSON document: %w", err)
	}
	return document, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeDocument(writer http.ResponseWriter, status int, document bson.M) {
	writeExtJSON(writer, status, bson.M{"data": document})
}

func writeDocuments(writer http.ResponseWriter, status int, documents []bson.M) {
	writeExtJSON(writer, status, bson.M{"data": documents, "meta": bson.M{"count": len(documents)}})
}

func writeWriteResult(writer http.ResponseWriter, status int, result memory.WriteResult) {
	data := bson.M{}
	if result.ID != nil {
		data["id"] = result.ID
	}
	if result.ID != nil {
		data["created"] = result.Created
	}
	writeExtJSON(writer, status, bson.M{"data": data})
}

func writeExtJSON(writer http.ResponseWriter, status int, value any) {
	data, err := bson.MarshalExtJSON(value, false, false)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "encoding_error", "the response could not be encoded")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(append(data, '\n'))
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
