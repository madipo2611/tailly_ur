package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateEntry struct {
	count int
	reset time.Time
}
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}

func NewRateLimiter() *RateLimiter { return &RateLimiter{entries: map[string]rateEntry{}} }
func (l *RateLimiter) Allow(r *http.Request) bool {
	limit := 60
	if strings.HasPrefix(r.URL.Path, "/v1/auth/") {
		limit = 10
	}
	ip := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if ip == "" {
		var err error
		ip, _, err = net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
	}
	key := ip + ":" + r.URL.Path
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if len(l.entries) >= 10000 {
		for oldKey, old := range l.entries {
			if old.reset.Before(now) {
				delete(l.entries, oldKey)
			}
		}
	}
	if e.reset.Before(now) {
		e = rateEntry{reset: now.Add(time.Minute)}
	}
	if e.count >= limit {
		l.entries[key] = e
		return false
	}
	e.count++
	l.entries[key] = e
	return true
}
func withRateLimit(next http.Handler) http.Handler {
	limiter := NewRateLimiter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") && !limiter.Allow(r) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
