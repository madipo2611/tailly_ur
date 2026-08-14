package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"digital-notary/internal/auth"
	"digital-notary/internal/billing"
	"digital-notary/internal/service"
	"digital-notary/internal/templates"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func New(a *service.App, sber *auth.Sber) http.Handler {
	m := http.NewServeMux()
	metrics := &metrics{}
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.Ready(ctx); err != nil {
			reply(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
			return
		}
		reply(w, 200, map[string]string{"status": "ready"})
	})
	m.HandleFunc("GET /metrics", metricsHandler(metrics))
	m.HandleFunc("GET /v1/billing/plans", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"plans": billing.Plans()})
	})
	m.HandleFunc("GET /v1/templates", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"templates": templates.Catalog()})
	})
	m.HandleFunc("GET /v1/templates/{code}", func(w http.ResponseWriter, r *http.Request) {
		template, ok := templates.ByCode(r.PathValue("code"))
		if !ok {
			reply(w, http.StatusNotFound, map[string]string{"error": "template not found"})
			return
		}
		reply(w, http.StatusOK, template)
	})
	m.HandleFunc("POST /v1/billing/webhook", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("BILLING_WEBHOOK_SECRET")
		if secret == "" {
			reply(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			respond(w, nil, err)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Billing-Signature"))) {
			reply(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook signature"})
			return
		}
		var update billing.SubscriptionUpdate
		if err := json.Unmarshal(body, &update); err != nil {
			respond(w, nil, err)
			return
		}
		if err := a.ApplySubscription(update); err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, http.StatusNoContent, nil)
	})
	m.HandleFunc("GET /v1/billing/subscription", func(w http.ResponseWriter, r *http.Request) {
		subscription, err := a.Subscription(user(a, r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"subscription": subscription})
	})
	m.HandleFunc("GET /v1/auth/providers", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"providers": map[string]bool{"sber": sber != nil}})
	})
	m.HandleFunc("GET /v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		actor := user(a, r)
		if actor == "" {
			reply(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		reply(w, http.StatusOK, map[string]string{"phone": actor, "provider": "sber"})
	})
	m.HandleFunc("GET /v1/auth/sber/start", func(w http.ResponseWriter, r *http.Request) {
		if sber == nil {
			reply(w, http.StatusServiceUnavailable, map[string]string{"error": "Sber ID is not configured"})
			return
		}
		if err := sber.Start(w, r); err != nil {
			respond(w, nil, err)
		}
	})
	m.HandleFunc("GET /v1/auth/sber/callback", func(w http.ResponseWriter, r *http.Request) {
		if sber == nil {
			reply(w, http.StatusServiceUnavailable, map[string]string{"error": "Sber ID is not configured"})
			return
		}
		phone, returnTo, err := sber.Complete(w, r)
		if err != nil {
			respond(w, nil, err)
			return
		}
		token, err := a.CreateSession(phone)
		if err != nil {
			respond(w, nil, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "nota_session", Value: token, Path: "/", MaxAge: 86400, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		if returnTo == "" {
			returnTo = "/"
		}
		http.Redirect(w, r, returnTo, http.StatusFound)
	})
	m.HandleFunc("POST /v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		err := a.Logout(sessionToken(r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "nota_session", Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		reply(w, http.StatusNoContent, nil)
	})
	m.HandleFunc("POST /v1/auth/logout-all", func(w http.ResponseWriter, r *http.Request) {
		err := a.LogoutAll(user(a, r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, http.StatusNoContent, nil)
	})
	m.HandleFunc("POST /v1/documents", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var x struct {
			Title, Template, ContractorPhone, Content, EDOAgreementVersion string
			AmountKopecks                                                  int64
		}
		if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
			respond(w, nil, err)
			return
		}
		d, e := a.CreateDocument(user(a, r), x.Title, x.Template, x.ContractorPhone, x.Content, x.EDOAgreementVersion, x.AmountKopecks)
		respond(w, d, e)
	})
	m.HandleFunc("GET /v1/documents", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		documents, err := a.List(user(a, r), limit)
		if err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"documents": documents})
	})
	m.HandleFunc("POST /v1/documents/{id}/send", func(w http.ResponseWriter, r *http.Request) {
		u, e := a.Send(r.PathValue("id"), user(a, r))
		respond(w, map[string]string{"signingUrl": u}, e)
	})
	m.HandleFunc("PUT /v1/documents/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			respond(w, nil, err)
			return
		}
		d, e := a.Upload(r.PathValue("id"), user(a, r), data)
		respond(w, d, e)
	})
	m.HandleFunc("GET /v1/documents/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		data, err := a.Download(r.PathValue("id"), user(a, r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=document.bin")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	m.HandleFunc("GET /v1/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, e, err := a.Get(r.PathValue("id"), user(a, r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, 200, map[string]any{"document": d, "audit": e})
	})
	m.HandleFunc("GET /v1/documents/{id}/evidence", func(w http.ResponseWriter, r *http.Request) {
		d, audit, signatures, err := a.Evidence(r.PathValue("id"), user(a, r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"document": d, "audit": audit, "signatures": signatures})
	})
	m.HandleFunc("GET /v1/documents/{id}/evidence/verify", func(w http.ResponseWriter, r *http.Request) {
		valid, err := a.VerifyAudit(r.PathValue("id"), user(a, r))
		if err != nil {
			respond(w, nil, err)
			return
		}
		reply(w, http.StatusOK, map[string]bool{"valid": valid})
	})
	m.HandleFunc("POST /v1/signing/{token}/pep/request", func(w http.ResponseWriter, r *http.Request) {
		c, e := a.RequestPEP(r.PathValue("token"))
		respond(w, map[string]string{"developmentCode": c}, e)
	})
	m.HandleFunc("POST /v1/signing/{token}/pep/confirm", func(w http.ResponseWriter, r *http.Request) {
		var x struct {
			Code              string
			AgreementAccepted bool
		}
		json.NewDecoder(r.Body).Decode(&x)
		d, e := a.ConfirmPEP(r.PathValue("token"), user(a, r), x.Code, r.RemoteAddr, r.UserAgent(), x.AgreementAccepted)
		respond(w, d, e)
	})
	m.HandleFunc("POST /v1/documents/{id}/ukep/start", func(w http.ResponseWriter, r *http.Request) {
		var x struct{ ProviderReference string }
		json.NewDecoder(r.Body).Decode(&x)
		d, e := a.StartUKEP(r.PathValue("id"), user(a, r), x.ProviderReference)
		respond(w, d, e)
	})
	m.HandleFunc("POST /v1/documents/{id}/ukep/complete", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusGone, map[string]string{"error": "UKЭП completion is accepted only through the provider callback"})
	})
	m.HandleFunc("POST /v1/integrations/ukep/callback", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("UKEP_WEBHOOK_SECRET")
		if secret == "" {
			reply(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			respond(w, nil, err)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-UKEP-Signature"))) {
			reply(w, http.StatusUnauthorized, map[string]string{"error": "invalid callback signature"})
			return
		}
		var event struct{ DocumentID, CustomerID, ProviderReference, Status string }
		if err := json.Unmarshal(body, &event); err != nil {
			respond(w, nil, err)
			return
		}
		if event.Status != "signed" {
			reply(w, http.StatusAccepted, map[string]string{"status": "ignored"})
			return
		}
		d, err := a.CompleteUKEP(event.DocumentID, event.CustomerID, event.ProviderReference)
		respond(w, d, err)
	})
	return withObservability(withRateLimit(m), metrics)
}
func user(a *service.App, r *http.Request) string {
	return a.User(sessionToken(r))
}
func sessionToken(r *http.Request) string {
	if bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); bearer != "" {
		return bearer
	}
	if c, err := r.Cookie("nota_session"); err == nil {
		return c.Value
	}
	return ""
}
func reply(w http.ResponseWriter, c int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c)
	json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, v any, e error) {
	if e != nil {
		reply(w, 400, map[string]string{"error": e.Error()})
		return
	}
	reply(w, 200, v)
}
