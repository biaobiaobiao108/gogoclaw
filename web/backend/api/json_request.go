package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const maxJSONRequestBodyBytes int64 = 1 << 20

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBodyBytes)
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "Request body exceeds 1MB limit", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
}
