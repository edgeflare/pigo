package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/google/uuid"
)

const loggerMessage = "http"

// ResponseRecorder is a wrapper for http.ResponseWriter to capture status codes and durations.
type ResponseRecorder struct {
	start time.Time
	http.ResponseWriter
	StatusCode int
}

func NewResponseRecorder(w http.ResponseWriter) *ResponseRecorder {
	return &ResponseRecorder{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
		start:          time.Now(),
	}
}

func (rr *ResponseRecorder) WriteHeader(statusCode int) {
	rr.StatusCode = statusCode
	rr.ResponseWriter.WriteHeader(statusCode)
}

func (rr *ResponseRecorder) Write(b []byte) (int, error) {
	return rr.ResponseWriter.Write(b)
}

// LoggerOptions defines configuration for the logger middleware.
type LoggerOptions struct {
	Logger *slog.Logger
	Format func(reqID string, rec *ResponseRecorder, r *http.Request, latency time.Duration) []slog.Attr
}

// logLevel returns an appropriate slog level for the HTTP status code.
func logLevel(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func LoggerWithOptions(options *LoggerOptions) func(http.Handler) http.Handler {
	if options == nil {
		options = &LoggerOptions{Logger: slog.Default()}
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Format == nil {
		options.Format = func(reqID string, rec *ResponseRecorder, r *http.Request, latency time.Duration) []slog.Attr {
			return []slog.Attr{
				slog.String("req_id", reqID),
				slog.Int("status", rec.StatusCode),
				slog.String("method", r.Method),
				slog.String("host", r.Host),
				slog.String("path", r.URL.Path),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
				slog.Duration("latency", latency),
			}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Value(httputil.LogEntryCtxKey).(*slog.Logger); ok {
				next.ServeHTTP(w, r)
				return
			}

			reqID, ok := r.Context().Value(httputil.RequestIDCtxKey).(string)
			if !ok {
				reqID = uuid.Nil.String()
			}

			rec := NewResponseRecorder(w)
			ctx := context.WithValue(r.Context(), httputil.LogEntryCtxKey, options.Logger)
			next.ServeHTTP(rec, r.WithContext(ctx))

			latency := time.Since(rec.start)

			pgRole, ok := r.Context().Value(httputil.OIDCRoleClaimCtxKey).(string)
			if !ok {
				options.Logger.Debug("pg_role not set; logger middleware may be ordered before AuthzFunc")
				pgRole = "unknown"
			}

			attrs := options.Format(reqID, rec, r, latency)
			attrs = append(attrs, slog.String("pg_role", pgRole))

			options.Logger.LogAttrs(r.Context(), logLevel(rec.StatusCode), loggerMessage, attrs...)
		})
	}
}
