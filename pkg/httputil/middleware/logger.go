package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

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

// Retrieve log metadata from context
func GetLogEntryMetadata(ctx context.Context) map[string]interface{} {
	if metadata, ok := ctx.Value(httputil.LogEntryCtxKey).(map[string]interface{}); ok {
		return metadata
	}
	return nil
}

// LoggerOptions defines configuration for the logger middleware.
type LoggerOptions struct {
	Logger *zap.Logger
	Format func(reqID string, rec *ResponseRecorder, r *http.Request, latency time.Duration) []zap.Field
}

var defaultLogger *zap.Logger

func init() {
	var err error
	defaultLogger, err = zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer defaultLogger.Sync()
}

func LoggerWithOptions(options *LoggerOptions) func(http.Handler) http.Handler {
	if options == nil {
		options = &LoggerOptions{Logger: defaultLogger}
	}

	if options.Format == nil {
		options.Format = func(reqID string, rec *ResponseRecorder, r *http.Request, latency time.Duration) []zap.Field {
			return []zap.Field{
				zap.String("req_id", reqID),
				zap.Int("status", rec.StatusCode),
				zap.String("method", r.Method),
				zap.String("host", r.Host),
				zap.String("url", r.URL.String()),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
				zap.Duration("latency", latency),
			}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			if _, ok := r.Context().Value(httputil.LogEntryCtxKey).(*zap.Logger); !ok {
				reqID, ok := r.Context().Value(httputil.RequestIDCtxKey).(string)
				if !ok {
					reqID = uuid.Nil.String()
				}

				rec := NewResponseRecorder(w)
				// try to minimize the data passed via context
				ctx := context.WithValue(r.Context(), httputil.LogEntryCtxKey, options.Logger)
				r = r.WithContext(ctx)

				next.ServeHTTP(rec, r)

				latency := time.Since(start)

				pgRole, ok := r.Context().Value(httputil.OIDCRoleClaimCtxKey).(string)
				if !ok {
					fmt.Println("pg_role isn't set. to fix add logger after all middleware.AuthzFunc{}")
					pgRole = "unknown"
				}

				fields := options.Format(reqID, rec, r, latency)
				fields = append(fields, zap.String("pg_role", pgRole))
				options.Logger.Info("response", fields...)
			} else {
				next.ServeHTTP(w, r)
			}
		})
	}
}
