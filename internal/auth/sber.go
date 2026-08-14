package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const stateCookie = "sber_oidc_state"

type Sber struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	stateKey []byte
}

type state struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"returnTo"`
	Expires  int64  `json:"expires"`
}

// NewSberFromEnv returns nil when Sber ID is not configured. Endpoint values
// come from the Sber ID partner cabinet, so production and test environments
// can be selected without code changes.
func NewSberFromEnv() (*Sber, error) {
	get := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	values := []string{get("SBER_ID_CLIENT_ID"), get("SBER_ID_CLIENT_SECRET"), get("SBER_ID_REDIRECT_URI"), get("SBER_ID_AUTH_URL"), get("SBER_ID_TOKEN_URL"), get("SBER_ID_ISSUER"), get("SBER_ID_JWKS_URL"), get("OIDC_STATE_HMAC_KEY")}
	allEmpty := true
	for _, v := range values {
		allEmpty = allEmpty && v == ""
	}
	if allEmpty {
		return nil, nil
	}
	for _, v := range values {
		if v == "" {
			return nil, errors.New("incomplete Sber ID OIDC configuration")
		}
	}
	key, err := base64.RawStdEncoding.DecodeString(get("OIDC_STATE_HMAC_KEY"))
	if err != nil || len(key) < 32 {
		return nil, errors.New("OIDC_STATE_HMAC_KEY must be a base64-encoded key of at least 32 bytes")
	}
	providerKeys := oidc.NewRemoteKeySet(context.Background(), get("SBER_ID_JWKS_URL"))
	return &Sber{
		config:   oauth2.Config{ClientID: get("SBER_ID_CLIENT_ID"), ClientSecret: get("SBER_ID_CLIENT_SECRET"), RedirectURL: get("SBER_ID_REDIRECT_URI"), Endpoint: oauth2.Endpoint{AuthURL: get("SBER_ID_AUTH_URL"), TokenURL: get("SBER_ID_TOKEN_URL")}, Scopes: []string{"openid", "profile", "phone"}},
		verifier: oidc.NewVerifier(get("SBER_ID_ISSUER"), providerKeys, &oidc.Config{ClientID: get("SBER_ID_CLIENT_ID")}),
		stateKey: key,
	}, nil
}

func (s *Sber) Start(w http.ResponseWriter, r *http.Request) error {
	st, err := newState()
	if err != nil {
		return err
	}
	if returnTo := r.URL.Query().Get("return_to"); strings.HasPrefix(returnTo, "/") && !strings.HasPrefix(returnTo, "//") {
		st.ReturnTo = returnTo
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	cookie := base64.RawURLEncoding.EncodeToString(raw) + "." + s.sign(raw)
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: cookie, Path: "/v1/auth/sber", MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, s.config.AuthCodeURL(st.State, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("nonce", st.Nonce), oauth2.SetAuthURLParam("code_challenge", challenge(st.Verifier)), oauth2.SetAuthURLParam("code_challenge_method", "S256")), http.StatusFound)
	return nil
}

func (s *Sber) Complete(w http.ResponseWriter, r *http.Request) (string, string, error) {
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		return "", "", errors.New("Sber ID authorization was declined: " + providerError)
	}
	st, err := s.readState(r)
	if err != nil {
		return "", "", err
	}
	if r.URL.Query().Get("state") != st.State {
		return "", "", errors.New("invalid OIDC state")
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		return "", "", errors.New("authorization code is missing")
	}
	token, err := s.config.Exchange(r.Context(), code, oauth2.SetAuthURLParam("code_verifier", st.Verifier))
	if err != nil {
		return "", "", errors.New("Sber ID token exchange failed")
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		return "", "", errors.New("Sber ID response has no id_token")
	}
	id, err := s.verifier.Verify(r.Context(), rawID)
	if err != nil {
		return "", "", errors.New("Sber ID token verification failed")
	}
	var claims struct {
		Subject       string `json:"sub"`
		Phone         string `json:"phone_number"`
		PhoneVerified *bool  `json:"phone_number_verified"`
		Nonce         string `json:"nonce"`
	}
	if err := id.Claims(&claims); err != nil {
		return "", "", errors.New("invalid Sber ID claims")
	}
	if claims.Subject == "" || claims.Nonce != st.Nonce {
		return "", "", errors.New("invalid Sber ID identity response")
	}
	if claims.PhoneVerified != nil && !*claims.PhoneVerified {
		return "", "", errors.New("Sber ID did not verify the mobile phone")
	}
	if claims.Phone == "" {
		return "", "", errors.New("Sber ID did not return a mobile phone; enable the phone scope for this client")
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/v1/auth/sber", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	return claims.Phone, st.ReturnTo, nil
}

func (s *Sber) readState(r *http.Request) (state, error) {
	c, err := r.Cookie(stateCookie)
	if err != nil {
		return state{}, errors.New("OIDC state cookie is missing")
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return state{}, errors.New("invalid OIDC state cookie")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !hmac.Equal([]byte(parts[1]), []byte(s.sign(raw))) {
		return state{}, errors.New("invalid OIDC state cookie")
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil || st.State == "" || st.Nonce == "" || st.Verifier == "" || time.Now().Unix() > st.Expires {
		return state{}, errors.New("expired OIDC state")
	}
	return st, nil
}
func (s *Sber) sign(raw []byte) string {
	h := hmac.New(sha256.New, s.stateKey)
	h.Write(raw)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func newState() (state, error) {
	a, err := random(32)
	if err != nil {
		return state{}, err
	}
	b, err := random(32)
	if err != nil {
		return state{}, err
	}
	c, err := random(48)
	if err != nil {
		return state{}, err
	}
	return state{State: a, Nonce: b, Verifier: c, Expires: time.Now().Add(10 * time.Minute).Unix()}, nil
}
func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ValidateConfig is useful for readiness and deployment checks.
func (s *Sber) ValidateConfig() error {
	if s == nil {
		return errors.New("Sber ID is not configured")
	}
	if _, err := url.ParseRequestURI(s.config.RedirectURL); err != nil {
		return err
	}
	return nil
}
