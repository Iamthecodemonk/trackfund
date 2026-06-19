package logger

import (
	"context"
	"net/http"
	"time"

	"log/slog"
	"os"
)

type Logger struct {
	l *slog.Logger
}

func New() *Logger {
	// Using os.Stdout explicitly so it defaults to writing to your terminal console
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})
	return &Logger{l: slog.New(handler)}
}

func (lg *Logger) Info(msg string, args ...slog.Attr) {
	lg.l.LogAttrs(context.Background(), slog.LevelInfo, msg, args...)
}

func (lg *Logger) Error(msg string, err error, args ...slog.Attr) {
	attrs := append(args, slog.Any("error", err))
	lg.l.LogAttrs(context.Background(), slog.LevelError, msg, attrs...)
}

func (lg *Logger) Debug(msg string, args ...slog.Attr) {
	lg.l.LogAttrs(context.Background(), slog.LevelDebug, msg, args...)
}

func (lg *Logger) Warn(msg string, args ...slog.Attr) {
	lg.l.LogAttrs(context.Background(), slog.LevelWarn, msg, args...)
}

// RequestMiddleware returns a middleware that logs request summary.
func (lg *Logger) RequestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{w: w, status: 200}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)

		lg.l.Info("http.request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("duration", duration),
			slog.String("remote", r.RemoteAddr),
		)
	})
}

type responseWriter struct {
	w      http.ResponseWriter
	status int
}

func (rw *responseWriter) Header() http.Header         { return rw.w.Header() }
func (rw *responseWriter) Write(b []byte) (int, error) { return rw.w.Write(b) }
func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.status = statusCode
	rw.w.WriteHeader(statusCode)
}

// WithContext returns the underlying slog.Logger (convenience).
func (lg *Logger) WithContext(ctx context.Context) *slog.Logger {
	return lg.l
}
