package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"digital-notary/internal/billing"
	"digital-notary/internal/domain"
	"digital-notary/internal/storage"
	"digital-notary/internal/templates"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type App struct {
	mu                  sync.RWMutex
	docs                map[string]*domain.Document
	tokens              map[string]signingLink
	challenges          map[string]otpChallenge
	loginChallenges     map[string]otpChallenge
	sessions            map[string]session
	signatures          []domain.Signature
	audit               []domain.AuditEvent
	objects             storage.ObjectStore
	persistence         StatePersistence
	signingURL, devCode string
}
type StatePersistence interface {
	SaveDocument(context.Context, domain.Document, domain.AuditEvent) error
	SaveStatus(context.Context, domain.Document, domain.AuditEvent) error
	LoadDocument(context.Context, string, string) (*domain.Document, []domain.AuditEvent, error)
	SaveSigningLink(context.Context, string, string, time.Time) error
	LoadSigningLink(context.Context, string) (string, time.Time, error)
	UseSigningLink(context.Context, string) error
	SaveOTP(context.Context, string, string, string, time.Time) error
	ValidateOTP(context.Context, string, string, string) (bool, error)
	SaveSession(context.Context, string, string, time.Time) error
	LoadSession(context.Context, string) (string, time.Time, error)
	RevokeSession(context.Context, string) error
	RevokeUserSessions(context.Context, string) error
	Healthy(context.Context) error
	SaveSignature(context.Context, domain.Signature) error
	LoadSignatures(context.Context, string) ([]domain.Signature, error)
	ListDocuments(context.Context, string, int) ([]domain.Document, error)
	Subscription(context.Context, string) (*billing.Subscription, error)
	UpsertSubscription(context.Context, billing.SubscriptionUpdate) error
}

func (a *App) Ready(ctx context.Context) error {
	if a.persistence != nil {
		return a.persistence.Healthy(ctx)
	}
	return nil
}

type signingLink struct {
	documentID string
	expiresAt  time.Time
}
type otpChallenge struct {
	digest    string
	expiresAt time.Time
	attempts  int
}
type session struct {
	userID    string
	expiresAt time.Time
}

func NewApp(base, code string) *App {
	return NewAppWithStore(base, code, storage.NewMemoryStore())
}
func NewAppWithStore(base, code string, objects storage.ObjectStore) *App {
	return NewAppWithPersistence(base, code, objects, nil)
}
func NewAppWithPersistence(base, code string, objects storage.ObjectStore, state StatePersistence) *App {
	if base == "" {
		base = "http://localhost:8080"
	}
	return &App{docs: map[string]*domain.Document{}, tokens: map[string]signingLink{}, challenges: map[string]otpChallenge{}, loginChallenges: map[string]otpChallenge{}, sessions: map[string]session{}, objects: objects, persistence: state, signingURL: base, devCode: code}
}
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func normalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) == 11 && digits[0] == '8' {
		digits = "7" + digits[1:]
	}
	if len(digits) == 11 && digits[0] == '7' {
		return "+" + digits
	}
	return phone
}
func validPhone(phone string) bool { return len(phone) == 12 && strings.HasPrefix(phone, "+7") }
func (a *App) RequestLogin(phone string) (string, error) {
	phone = normalizePhone(phone)
	if !validPhone(phone) {
		return "", errors.New("phone is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	code := a.devCode
	if code == "" {
		code = "000000"
	}
	a.loginChallenges[phone] = otpChallenge{digest: hash(code), expiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if a.persistence != nil {
		if err := a.persistence.SaveOTP(context.Background(), phone, "login", a.loginChallenges[phone].digest, a.loginChallenges[phone].expiresAt); err != nil {
			return "", err
		}
	}
	return code, nil
}
func (a *App) VerifyLogin(phone, code string) (string, error) {
	phone = normalizePhone(phone)
	if !validPhone(phone) {
		return "", errors.New("invalid phone")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	challenge, ok := a.loginChallenges[phone]
	if !ok && a.persistence != nil {
		if valid, err := a.persistence.ValidateOTP(context.Background(), phone, "login", hash(code)); err == nil && valid {
			challenge = otpChallenge{digest: hash(code), expiresAt: time.Now().UTC().Add(time.Second)}
			ok = true
		}
	}
	valid := ok && !time.Now().UTC().After(challenge.expiresAt) && challenge.digest == hash(code)
	if a.persistence != nil {
		persisted, err := a.persistence.ValidateOTP(context.Background(), phone, "login", hash(code))
		if err != nil {
			return "", err
		}
		valid = persisted
	}
	if !valid {
		return "", errors.New("invalid OTP")
	}
	delete(a.loginChallenges, phone)
	return a.createSessionLocked(phone)
}

// CreateSession issues an application session only for a phone number verified
// by an external identity provider. It intentionally has no development fallback.
func (a *App) CreateSession(phone string) (string, error) {
	phone = normalizePhone(phone)
	if !validPhone(phone) {
		return "", errors.New("verified Russian mobile phone is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.createSessionLocked(phone)
}
func (a *App) createSessionLocked(phone string) (string, error) {
	token := newID()
	a.sessions[token] = session{userID: phone, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	if a.persistence != nil {
		if err := a.persistence.SaveSession(context.Background(), token, phone, a.sessions[token].expiresAt); err != nil {
			return "", err
		}
	}
	return token, nil
}
func (a *App) User(bearer string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.sessions[bearer]
	if !ok && a.persistence != nil {
		if user, expires, err := a.persistence.LoadSession(context.Background(), bearer); err == nil {
			s = session{userID: user, expiresAt: expires}
			ok = true
		}
	}
	if !ok || time.Now().UTC().After(s.expiresAt) {
		return ""
	}
	return s.userID
}
func (a *App) Logout(token string) error {
	if token == "" {
		return errors.New("missing session token")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
	if a.persistence != nil {
		return a.persistence.RevokeSession(context.Background(), token)
	}
	return nil
}
func (a *App) LogoutAll(actor string) error {
	if actor == "" {
		return errors.New("unauthorized")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, s := range a.sessions {
		if s.userID == actor {
			delete(a.sessions, token)
		}
	}
	if a.persistence != nil {
		return a.persistence.RevokeUserSessions(context.Background(), actor)
	}
	return nil
}
func (a *App) CreateDocument(customerID, title, template, phone, content, agreement string, amount int64) (*domain.Document, error) {
	phone = normalizePhone(phone)
	if customerID == "" || title == "" || !validPhone(phone) || content == "" || agreement == "" {
		return nil, errors.New("customer, title, contractor phone, content and EDO agreement are required")
	}
	if len(title) > 300 || len(content) > 1<<20 {
		return nil, errors.New("document title or content exceeds the allowed size")
	}
	templateMetadata, ok := templates.ByCode(template)
	if !ok {
		return nil, errors.New("unknown document template")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := &domain.Document{ID: newID(), Title: title, Template: template, TemplateVersion: templateMetadata.Version, CustomerID: customerID, ContractorPhone: phone, ContentHash: hash(content), AmountKopecks: amount, Status: domain.StatusDraft, AgreementVersion: agreement, CreatedAt: time.Now().UTC()}
	a.docs[d.ID] = d
	event := a.auditLocked(d.ID, customerID, "document.created", d.ContentHash)
	if a.persistence != nil {
		if err := a.persistence.SaveDocument(context.Background(), *d, event); err != nil {
			return nil, err
		}
	}
	return d, nil
}
func (a *App) Send(id, actor string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.documentLocked(id, actor)
	if d == nil || d.CustomerID != actor {
		return "", errors.New("document not found")
	}
	if d.Status != domain.StatusDraft {
		return "", errors.New("document is not a draft")
	}
	t := newID() + newID()
	a.tokens[t] = signingLink{documentID: id, expiresAt: time.Now().UTC().Add(48 * time.Hour)}
	if a.persistence != nil {
		if err := a.persistence.SaveSigningLink(context.Background(), t, id, a.tokens[t].expiresAt); err != nil {
			return "", err
		}
	}
	d.Status = domain.StatusSent
	event := a.auditLocked(id, actor, "document.sent", hash(t))
	if a.persistence != nil {
		if err := a.persistence.SaveStatus(context.Background(), *d, event); err != nil {
			return "", err
		}
	}
	return a.signingURL + "/sign/" + t, nil
}
func (a *App) RequestPEP(token string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	link, ok := a.tokens[token]
	if !ok && a.persistence != nil {
		if doc, expiry, err := a.persistence.LoadSigningLink(context.Background(), token); err == nil {
			link = signingLink{documentID: doc, expiresAt: expiry}
			ok = true
		}
	}
	if !ok || time.Now().UTC().After(link.expiresAt) {
		return "", errors.New("invalid signing link")
	}
	code := a.devCode
	if code == "" {
		code = ""
		for i := 0; i < 6; i++ {
			code += fmt.Sprint(time.Now().UnixNano() % 10)
		}
	}
	a.challenges[token] = otpChallenge{digest: hash(code), expiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if a.persistence != nil {
		if err := a.persistence.SaveOTP(context.Background(), token, "pep", a.challenges[token].digest, a.challenges[token].expiresAt); err != nil {
			return "", err
		}
	}
	a.auditLocked(link.documentID, "contractor", "pep.challenge.requested", hash(token))
	return code, nil
}

func (a *App) Upload(id, actor string, data []byte) (*domain.Document, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.documentLocked(id, actor)
	if d == nil || d.CustomerID != actor {
		return nil, errors.New("document not found")
	}
	if d.Status != domain.StatusDraft {
		return nil, errors.New("only a draft can be replaced")
	}
	key := "documents/" + d.ID + "/original"
	digest, err := a.objects.Put(key, data)
	if err != nil {
		return nil, err
	}
	d.ObjectKey, d.ContentHash = key, digest
	event := a.auditLocked(d.ID, actor, "document.file.uploaded", digest)
	if a.persistence != nil {
		if err := a.persistence.SaveStatus(context.Background(), *d, event); err != nil {
			return nil, err
		}
	}
	return d, nil
}
func (a *App) Download(id, actor string) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.docs[id]
	if d == nil && a.persistence != nil {
		loaded, _, err := a.persistence.LoadDocument(context.Background(), id, actor)
		if err == nil {
			d = loaded
			a.docs[id] = d
		}
	}
	if d == nil || actor == "" || (actor != d.CustomerID && actor != d.ContractorPhone) {
		return nil, errors.New("document not found")
	}
	if d.ObjectKey == "" {
		return nil, errors.New("document has no uploaded file")
	}
	data, err := a.objects.Get(d.ObjectKey)
	if err != nil {
		return nil, err
	}
	if hash(string(data)) != d.ContentHash {
		return nil, errors.New("stored file integrity check failed")
	}
	return data, nil
}
func (a *App) ConfirmPEP(token, contractorID, code, ip, ua string, agreementAccepted bool) (*domain.Document, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	link, validLink := a.tokens[token]
	if !validLink && a.persistence != nil {
		if doc, expiry, err := a.persistence.LoadSigningLink(context.Background(), token); err == nil {
			link = signingLink{documentID: doc, expiresAt: expiry}
			validLink = true
		}
	}
	docID := link.documentID
	d := a.docs[docID]
	if d == nil && a.persistence != nil && contractorID != "" {
		if loaded, _, err := a.persistence.LoadDocument(context.Background(), docID, contractorID); err == nil {
			d = loaded
			a.docs[docID] = d
		}
	}
	challenge, validChallenge := a.challenges[token]
	if !validChallenge && a.persistence != nil {
		if valid, err := a.persistence.ValidateOTP(context.Background(), token, "pep", hash(code)); err == nil && valid {
			challenge = otpChallenge{digest: hash(code), expiresAt: time.Now().UTC().Add(time.Second)}
			validChallenge = true
		}
	}
	validOTP := validChallenge && !time.Now().UTC().After(challenge.expiresAt) && challenge.digest == hash(code)
	if a.persistence != nil {
		persisted, err := a.persistence.ValidateOTP(context.Background(), token, "pep", hash(code))
		if err != nil {
			return nil, err
		}
		validOTP = persisted
	}
	if !validLink || time.Now().UTC().After(link.expiresAt) || d == nil || !validOTP {
		return nil, errors.New("invalid or expired OTP")
	}
	if contractorID == "" || !agreementAccepted {
		return nil, errors.New("contractor identity and EDO agreement acceptance are required")
	}
	if contractorID != d.ContractorPhone {
		return nil, errors.New("authenticated user does not match the document contractor")
	}
	if d.Status != domain.StatusSent {
		return nil, errors.New("document cannot be signed")
	}
	evidence := hash(d.ContentHash + contractorID + d.AgreementVersion + ip + ua + time.Now().UTC().String())
	signature := domain.Signature{DocumentID: docID, UserID: contractorID, Type: domain.PEP, EvidenceHash: evidence, SignedAt: time.Now().UTC()}
	a.signatures = append(a.signatures, signature)
	if a.persistence != nil {
		if err := a.persistence.SaveSignature(context.Background(), signature); err != nil {
			return nil, err
		}
	}
	d.Status = domain.StatusPepSigned
	delete(a.challenges, token)
	if a.persistence != nil {
		if err := a.persistence.UseSigningLink(context.Background(), token); err != nil {
			return nil, err
		}
	}
	a.auditLocked(docID, contractorID, "edo.agreement.accepted", hash(d.AgreementVersion))
	event := a.auditLocked(docID, contractorID, "pep.signed", evidence)
	if a.persistence != nil {
		if err := a.persistence.SaveStatus(context.Background(), *d, event); err != nil {
			return nil, err
		}
	}
	return d, nil
}
func (a *App) StartUKEP(docID, actor, providerRef string) (*domain.Document, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.documentLocked(docID, actor)
	if d == nil || d.CustomerID != actor {
		return nil, errors.New("document not found")
	}
	if d.Status != domain.StatusPepSigned {
		return nil, errors.New("PEP signature is required first")
	}
	d.Status = domain.StatusAwaitingUKEP
	event := a.auditLocked(docID, actor, "ukep.requested", hash(providerRef))
	if a.persistence != nil {
		if err := a.persistence.SaveStatus(context.Background(), *d, event); err != nil {
			return nil, err
		}
	}
	return d, nil
}
func (a *App) CompleteUKEP(docID, actor, providerRef string) (*domain.Document, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.documentLocked(docID, actor)
	if d == nil || d.CustomerID != actor || d.Status != domain.StatusAwaitingUKEP {
		return nil, errors.New("invalid UKЭП completion")
	}
	signature := domain.Signature{DocumentID: docID, UserID: actor, Type: domain.UKEP, ProviderReference: providerRef, EvidenceHash: hash(d.ContentHash + providerRef), SignedAt: time.Now().UTC()}
	a.signatures = append(a.signatures, signature)
	if a.persistence != nil {
		if err := a.persistence.SaveSignature(context.Background(), signature); err != nil {
			return nil, err
		}
	}
	d.Status = domain.StatusCompleted
	event := a.auditLocked(docID, actor, "ukep.signed", hash(providerRef))
	if a.persistence != nil {
		if err := a.persistence.SaveStatus(context.Background(), *d, event); err != nil {
			return nil, err
		}
	}
	return d, nil
}
func (a *App) documentLocked(id, actor string) *domain.Document {
	d := a.docs[id]
	if d == nil && a.persistence != nil && actor != "" {
		if loaded, _, err := a.persistence.LoadDocument(context.Background(), id, actor); err == nil {
			d = loaded
			a.docs[id] = d
		}
	}
	return d
}
func (a *App) Get(id, actor string) (*domain.Document, []domain.AuditEvent, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	d := a.docs[id]
	if d == nil {
		if a.persistence != nil {
			return a.persistence.LoadDocument(context.Background(), id, actor)
		}
		return nil, nil, errors.New("document not found")
	}
	if actor == "" || (actor != d.CustomerID && actor != d.ContractorPhone) {
		return nil, nil, errors.New("document not found")
	}
	e := []domain.AuditEvent{}
	for _, x := range a.audit {
		if x.DocumentID == id {
			e = append(e, x)
		}
	}
	return d, e, nil
}
func (a *App) List(actor string, limit int) ([]domain.Document, error) {
	if actor == "" {
		return nil, errors.New("unauthorized")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if a.persistence != nil {
		return a.persistence.ListDocuments(context.Background(), actor, limit)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []domain.Document{}
	for _, d := range a.docs {
		if d.CustomerID == actor || d.ContractorPhone == actor {
			out = append(out, *d)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
func (a *App) Subscription(actor string) (*billing.Subscription, error) {
	if actor == "" {
		return nil, errors.New("unauthorized")
	}
	if a.persistence == nil {
		return nil, nil
	}
	return a.persistence.Subscription(context.Background(), actor)
}
func (a *App) ApplySubscription(update billing.SubscriptionUpdate) error {
	if a.persistence == nil {
		return errors.New("billing storage is not configured")
	}
	return a.persistence.UpsertSubscription(context.Background(), update)
}
func (a *App) Evidence(id, actor string) (*domain.Document, []domain.AuditEvent, []domain.Signature, error) {
	d, audit, err := a.Get(id, actor)
	if err != nil {
		return nil, nil, nil, err
	}
	signatures := []domain.Signature{}
	for _, s := range a.signatures {
		if s.DocumentID == id {
			signatures = append(signatures, s)
		}
	}
	if a.persistence != nil {
		loaded, err := a.persistence.LoadSignatures(context.Background(), id)
		if err != nil {
			return nil, nil, nil, err
		}
		signatures = loaded
	}
	return d, audit, signatures, nil
}
func (a *App) VerifyAudit(id, actor string) (bool, error) {
	_, events, err := a.Get(id, actor)
	if err != nil {
		return false, err
	}
	previous := ""
	for _, e := range events {
		if e.PreviousHash != previous {
			return false, nil
		}
		expected := hash(e.ID + e.DocumentID + e.Action + e.PayloadHash + e.PreviousHash + e.OccurredAt.String())
		if e.Hash != expected {
			return false, nil
		}
		previous = e.Hash
	}
	return true, nil
}
func (a *App) auditLocked(doc, actor, action, payload string) domain.AuditEvent {
	prev := ""
	if n := len(a.audit); n > 0 {
		prev = a.audit[n-1].Hash
	}
	ev := domain.AuditEvent{ID: newID(), DocumentID: doc, ActorID: actor, Action: action, PayloadHash: payload, PreviousHash: prev, OccurredAt: time.Now().UTC()}
	ev.Hash = hash(ev.ID + ev.DocumentID + ev.Action + ev.PayloadHash + ev.PreviousHash + ev.OccurredAt.String())
	a.audit = append(a.audit, ev)
	return ev
}
