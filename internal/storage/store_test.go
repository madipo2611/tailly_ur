package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPStoreUploadsObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("unexpected request")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "document" {
			t.Fatal("unexpected body")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	s, err := NewHTTPStore(server.URL, "test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Put("documents/a/original", []byte("document")); err != nil || got == "" {
		t.Fatalf("Put = %q, %v", got, err)
	}
}
