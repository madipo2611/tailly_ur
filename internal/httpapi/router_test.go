package httpapi

import (
	"bytes"
	"digital-notary/internal/service"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCreateAndSendDocumentOverHTTP(t *testing.T) {
	app := service.NewApp("", "")
	h := New(app, nil)
	call := func(method, path, token string, payload any) *httptest.ResponseRecorder {
		var body *bytes.Reader
		if payload == nil {
			body = bytes.NewReader(nil)
		} else {
			raw, _ := json.Marshal(payload)
			body = bytes.NewReader(raw)
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	token, err := app.CreateSession("+79990000000")
	if err != nil {
		t.Fatal(err)
	}
	w := call("POST", "/v1/documents", token, map[string]any{"title": "Акт", "template": "act", "contractorPhone": "+79991111111", "content": "Работы выполнены", "edoAgreementVersion": "v1", "amountKopecks": 10000})
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var document map[string]any
	json.NewDecoder(w.Body).Decode(&document)
	id := document["ID"].(string)
	w = call("POST", "/v1/documents/"+id+"/send", token, nil)
	if w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
}
