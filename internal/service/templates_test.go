package service

import "testing"

func TestCreateDocumentRejectsUnknownTemplate(t *testing.T) {
	a := NewApp("", "")
	if _, err := a.CreateDocument("customer", "Document", "unknown", "+79990000000", "content", "v1", 0); err == nil {
		t.Fatal("expected unknown template to be rejected")
	}
}
