package httpapi

import (
	"bytes"
	"digital-notary/internal/service"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCreateAndSendDocumentOverHTTP(t *testing.T) {
	h := New(service.NewApp("", "000000"))
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
	if w := call("POST", "/v1/auth/sms/request", "", map[string]string{"phone": "+79990000000"}); w.Code != 200 {
		t.Fatalf("request otp: %d", w.Code)
	}
	w := call("POST", "/v1/auth/sms/verify", "", map[string]string{"phone": "+79990000000", "code": "000000"})
	if w.Code != 200 {
		t.Fatalf("verify: %d", w.Code)
	}
	var login map[string]string
	json.NewDecoder(w.Body).Decode(&login)
	w = call("POST", "/v1/documents", login["accessToken"], map[string]any{"title": "Акт", "template": "act", "contractorPhone": "+79991111111", "content": "Работы выполнены", "edoAgreementVersion": "v1", "amountKopecks": 10000})
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var document map[string]any
	json.NewDecoder(w.Body).Decode(&document)
	id := document["ID"].(string)
	w = call("POST", "/v1/documents/"+id+"/send", login["accessToken"], nil)
	if w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
}
