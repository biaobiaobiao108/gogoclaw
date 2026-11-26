package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxJSONRequestBodyBytes int64 = 1 << 20

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSONRequestWithLimit(w, r, maxJSONRequestBodyBytes, dst)
}

func decodeJSONRequestWithLimit(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return json.NewDecoder(r.Body).Decode(dst)
}

func readJSONRequestBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func writeJSONReadError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "Request body exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "Failed to read request body", http.StatusBadRequest)
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "Request body exceeds 1MB limit", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
}
