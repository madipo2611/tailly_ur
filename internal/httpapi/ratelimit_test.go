package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestRateLimiterLimitsAuth(t *testing.T) {
	l := NewRateLimiter()
	for i := 0; i < 10; i++ {
		r := httptest.NewRequest("POST", "/v1/auth/sms/request", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		if !l.Allow(r) {
			t.Fatalf("attempt %d rejected", i)
		}
	}
	r := httptest.NewRequest("POST", "/v1/auth/sms/request", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	if l.Allow(r) {
		t.Fatal("expected rate limit")
	}
}
