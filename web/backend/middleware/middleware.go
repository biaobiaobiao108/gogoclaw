package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

// JSONContentType sets JSON content and prevents API responses from being
// cached by browsers or shared proxies.
func JSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w = &apiJSONResponseWriter{ResponseWriter: w}
		}
		next.ServeHTTP(w, r)
	})
}

// apiJSONResponseWriter converts plain-text API error bodies emitted by
// http.Error into JSON while preserving status codes and streaming support.
type apiJSONResponseWriter struct {
	http.ResponseWriter
	statusCode int
	errorBody  bool
}

func (w *apiJSONResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *apiJSONResponseWriter) Write(data []byte) (int, error) {
	if w.statusCode >= http.StatusBadRequest && !w.errorBody {
		w.errorBody = true
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) > 0 && !json.Valid(trimmed) {
			encoded, err := json.Marshal(map[string]string{"error": string(trimmed)})
			if err != nil {
				return 0, err
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.ResponseWriter.Write(encoded); err != nil {
				return 0, err
			}
			return len(data), nil
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *apiJSONResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *apiJSONResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *apiJSONResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// responseRecorder wraps http.ResponseWriter to capture the status code.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
func (rr *responseRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter so that http.ResponseController
// and interface checks (like http.Flusher) can see through the wrapper.
func (rr *responseRecorder) Unwrap() http.ResponseWriter {
	return rr.ResponseWriter
}

// Hijack implements http.Hijacker so that WebSocket upgrades work through
// the middleware layer.
func (rr *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rr.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Logger logs each HTTP request with method, path, status code, and duration.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.DebugC("http", fmt.Sprintf("%s %s %d %s", r.Method, r.URL.Path, rec.statusCode, time.Since(start)))
	})
}

// Recoverer recovers from panics in downstream handlers and returns a 500
// Internal Server Error response.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.RecoverPanicNoExit(err)
				logger.ErrorC("http", fmt.Sprintf("panic recovered: %v\n%s", err, debug.Stack()))
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
