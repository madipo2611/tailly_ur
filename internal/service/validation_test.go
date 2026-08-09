package service

import (
	"strings"
	"testing"
)

func TestCreateDocumentRejectsLargeContent(t *testing.T) {
	a := NewApp("", "")
	_, err := a.CreateDocument("customer", "Document", "act", "+79990000000", strings.Repeat("x", (1<<20)+1), "v1", 0)
	if err == nil {
		t.Fatal("expected oversized content rejection")
	}
}
func TestNormalizePhone(t *testing.T) {
	if got := normalizePhone("8 (999) 123-45-67"); got != "+79991234567" {
		t.Fatalf("got %s", got)
	}
}
func TestCreateDocumentRejectsInvalidPhone(t *testing.T) {
	a := NewApp("", "")
	if _, err := a.CreateDocument("customer", "Document", "act", "not-a-phone", "content", "v1", 0); err == nil {
		t.Fatal("expected invalid phone rejection")
	}
}
