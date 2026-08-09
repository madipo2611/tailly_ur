package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

type metrics struct{ requests, failures, latencyNanos atomic.Uint64 }
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(s int) { w.status = s; w.ResponseWriter.WriteHeader(s) }
func withObservability(next http.Handler, m *metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			b := make([]byte, 12)
			_, _ = rand.Read(b)
			requestID = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", requestID)
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		elapsed := time.Since(start)
		m.requests.Add(1)
		m.latencyNanos.Add(uint64(elapsed))
		if rw.status >= 500 {
			m.failures.Add(1)
		}
		log.Printf("request_id=%s method=%s path=%s status=%d duration_ms=%d", requestID, r.Method, r.URL.Path, rw.status, elapsed.Milliseconds())
	})
}
func metricsHandler(m *metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		n := m.requests.Load()
		fmt.Fprintf(w, "digital_notary_http_requests_total %d\n", n)
		fmt.Fprintf(w, "digital_notary_http_failures_total %d\n", m.failures.Load())
		if n > 0 {
			fmt.Fprintf(w, "digital_notary_http_request_duration_seconds_avg %.6f\n", float64(m.latencyNanos.Load())/float64(n)/1e9)
		}
	}
}
