package main

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

func withHTTPLogging(name string, next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "handler unavailable", http.StatusServiceUnavailable)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		log := zap.L().With(
			zap.String("interface", name),
			zap.String("remote", r.RemoteAddr),
			zap.String("method", r.Method),
			zap.String("path", r.URL.RequestURI()),
			zap.String("host", r.Host),
			zap.String("user_agent", r.UserAgent()),
			zap.Int64("content_length", r.ContentLength),
		)
		log.Debug("http request started")

		rec := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Debug(
			"http request completed",
			zap.Int("status", status),
			zap.Int("bytes", rec.bytes),
			zap.Duration("latency", time.Since(started)),
		)
	})
}
