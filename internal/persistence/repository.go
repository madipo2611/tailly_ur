package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"digital-notary/internal/billing"
	"digital-notary/internal/domain"
	"digital-notary/internal/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository writes the business record, audit evidence and outbox event in one
// transaction. A publisher can safely retry unprocessed outbox rows later.
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository      { return &Repository{pool: pool} }
func (r *Repository) Healthy(ctx context.Context) error { return r.pool.Ping(ctx) }
func (r *Repository) SaveSignature(ctx context.Context, s domain.Signature) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	signer, err := ensureUser(ctx, tx, s.UserID)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(map[string]string{"evidenceHash": s.EvidenceHash})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO signatures(document_id,signer_id,kind,evidence,provider_reference,signed_at) VALUES($1,$2,$3,$4,$5,$6)`, s.DocumentID, signer, s.Type, evidence, s.ProviderReference, s.SignedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) LoadSignatures(ctx context.Context, documentID string) ([]domain.Signature, error) {
	rows, err := r.pool.Query(ctx, `SELECT s.document_id,u.phone,s.kind,COALESCE(s.provider_reference,''),s.evidence->>'evidenceHash',s.signed_at FROM signatures s JOIN users u ON u.id=s.signer_id WHERE s.document_id=$1 ORDER BY s.signed_at`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Signature{}
	for rows.Next() {
		var s domain.Signature
		if err := rows.Scan(&s.DocumentID, &s.UserID, &s.Type, &s.ProviderReference, &s.EvidenceHash, &s.SignedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *Repository) ListDocuments(ctx context.Context, actor string, limit int) ([]domain.Document, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.id,d.title,COALESCE(d.template_code,''),d.template_version,u.phone,d.contractor_phone,d.content_sha256,d.object_key,d.amount_kopecks,d.status,d.edo_agreement_version,d.created_at FROM documents d JOIN users u ON u.id=d.customer_id WHERE u.phone=$1 OR d.contractor_phone=$1 ORDER BY d.created_at DESC LIMIT $2`, actor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Document{}
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(&d.ID, &d.Title, &d.Template, &d.TemplateVersion, &d.CustomerID, &d.ContractorPhone, &d.ContentHash, &d.ObjectKey, &d.AmountKopecks, &d.Status, &d.AgreementVersion, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *Repository) Subscription(ctx context.Context, phone string) (*billing.Subscription, error) {
	s := &billing.Subscription{}
	err := r.pool.QueryRow(ctx, `SELECT s.plan_code,s.status,s.documents_used,s.current_period_ends_at FROM subscriptions s JOIN users u ON u.id=s.user_id WHERE u.phone=$1`, phone).Scan(&s.PlanCode, &s.Status, &s.DocumentsUsed, &s.CurrentPeriodEndsAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (r *Repository) UpsertSubscription(ctx context.Context, update billing.SubscriptionUpdate) error {
	if billing.DocumentLimit(update.PlanCode) == 0 && update.PlanCode != "enterprise" {
		return fmt.Errorf("unknown plan %s", update.PlanCode)
	}
	if !billing.ValidStatus(update.Status) {
		return fmt.Errorf("unknown subscription status %s", update.Status)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, err := ensureUser(ctx, tx, update.Phone)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO subscriptions(user_id,plan_code,status,current_period_ends_at) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET plan_code=EXCLUDED.plan_code,status=EXCLUDED.status,current_period_ends_at=EXCLUDED.current_period_ends_at,documents_used=CASE WHEN subscriptions.plan_code IS DISTINCT FROM EXCLUDED.plan_code OR subscriptions.current_period_ends_at IS DISTINCT FROM EXCLUDED.current_period_ends_at THEN 0 ELSE subscriptions.documents_used END,updated_at=now()`, id, update.PlanCode, update.Status, update.CurrentPeriodEndsAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) CleanupExpired(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{`DELETE FROM otp_challenges WHERE expires_at<now()`, `DELETE FROM signing_links WHERE expires_at<now() OR used_at<now()-interval '7 days'`, `DELETE FROM sessions WHERE expires_at<now() OR revoked_at<now()-interval '7 days'`} {
		if _, err = tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) SaveDocument(ctx context.Context, d domain.Document, event domain.AuditEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	customerID, err := ensureUser(ctx, tx, d.CustomerID)
	if err != nil {
		return err
	}
	if err = consumeDocumentAllowance(ctx, tx, customerID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO documents(id,customer_id,contractor_phone,title,template_code,template_version,object_key,content_sha256,amount_kopecks,status,edo_agreement_version,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, d.ID, customerID, d.ContractorPhone, d.Title, d.Template, d.TemplateVersion, d.ObjectKey, d.ContentHash, d.AmountKopecks, d.Status, d.AgreementVersion, d.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert document: %w", err)
	}
	if err = insertAudit(ctx, tx, event); err != nil {
		return err
	}
	return insertOutboxAndCommit(ctx, tx, d.ID, "document.created", map[string]string{"documentId": d.ID, "status": string(d.Status)})
}
func consumeDocumentAllowance(ctx context.Context, tx pgx.Tx, userID string) error {
	var plan string
	var used int
	err := tx.QueryRow(ctx, `SELECT plan_code,documents_used FROM subscriptions WHERE user_id=$1 AND status IN ('active','trial') AND current_period_ends_at>now() FOR UPDATE`, userID).Scan(&plan, &used)
	if err != nil {
		return fmt.Errorf("active subscription required: %w", err)
	}
	limit := billing.DocumentLimit(plan)
	if limit > 0 && used >= limit {
		return fmt.Errorf("document limit reached for plan %s", plan)
	}
	_, err = tx.Exec(ctx, `UPDATE subscriptions SET documents_used=documents_used+1,updated_at=now() WHERE user_id=$1`, userID)
	return err
}

func (r *Repository) SaveStatus(ctx context.Context, d domain.Document, event domain.AuditEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE documents SET status=$2, object_key=$3, content_sha256=$4 WHERE id=$1`, d.ID, d.Status, d.ObjectKey, d.ContentHash); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, event); err != nil {
		return err
	}
	return insertOutboxAndCommit(ctx, tx, d.ID, "document.status_changed", map[string]string{"documentId": d.ID, "status": string(d.Status)})
}

func (r *Repository) LoadDocument(ctx context.Context, id, actor string) (*domain.Document, []domain.AuditEvent, error) {
	const query = `SELECT d.id,d.title,COALESCE(d.template_code,''),d.template_version,u.phone,d.contractor_phone,d.content_sha256,d.object_key,d.amount_kopecks,d.status,d.edo_agreement_version,d.created_at FROM documents d JOIN users u ON u.id=d.customer_id WHERE d.id=$1 AND (u.phone=$2 OR d.contractor_phone=$2)`
	d := &domain.Document{}
	if err := r.pool.QueryRow(ctx, query, id, actor).Scan(&d.ID, &d.Title, &d.Template, &d.TemplateVersion, &d.CustomerID, &d.ContractorPhone, &d.ContentHash, &d.ObjectKey, &d.AmountKopecks, &d.Status, &d.AgreementVersion, &d.CreatedAt); err != nil {
		return nil, nil, fmt.Errorf("document not found: %w", err)
	}
	rows, err := r.pool.Query(ctx, `SELECT e.id,e.document_id,COALESCE(u.phone,''),COALESCE(e.action,''),e.payload_sha256,COALESCE(e.previous_hash,''),e.event_hash,e.occurred_at FROM audit_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.document_id=$1 ORDER BY e.occurred_at`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	events := []domain.AuditEvent{}
	for rows.Next() {
		var e domain.AuditEvent
		if err := rows.Scan(&e.ID, &e.DocumentID, &e.ActorID, &e.Action, &e.PayloadHash, &e.PreviousHash, &e.Hash, &e.OccurredAt); err != nil {
			return nil, nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return d, events, nil
}

// PublishPending preserves at-least-once delivery. Kafka consumers must
// de-duplicate using the event_id header.
func (r *Repository) PublishPending(ctx context.Context, publisher events.Publisher, limit int) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,aggregate_id,type,payload FROM outbox_events WHERE published_at IS NULL ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	messages := []events.Message{}
	for rows.Next() {
		var m events.Message
		if err := rows.Scan(&m.ID, &m.AggregateID, &m.Type, &m.Payload); err != nil {
			return 0, err
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, m := range messages {
		if err := publisher.Publish(ctx, m); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox_events SET published_at=now() WHERE id=$1`, m.ID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(messages), nil
}

func (r *Repository) NotificationTargets(ctx context.Context, documentID string) (string, string, error) {
	var customer, contractor string
	err := r.pool.QueryRow(ctx, `SELECT u.phone,d.contractor_phone FROM documents d JOIN users u ON u.id=d.customer_id WHERE d.id=$1`, documentID).Scan(&customer, &contractor)
	return customer, contractor, err
}

func tokenHash(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }
func (r *Repository) SaveSigningLink(ctx context.Context, raw, documentID string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO signing_links(token_hash,document_id,expires_at) VALUES($1,$2,$3)`, tokenHash(raw), documentID, expiresAt)
	return err
}
func (r *Repository) LoadSigningLink(ctx context.Context, raw string) (string, time.Time, error) {
	var doc string
	var expires time.Time
	err := r.pool.QueryRow(ctx, `SELECT document_id,expires_at FROM signing_links WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now()`, tokenHash(raw)).Scan(&doc, &expires)
	return doc, expires, err
}
func (r *Repository) UseSigningLink(ctx context.Context, raw string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE signing_links SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL`, tokenHash(raw))
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("signing link already used")
	}
	return err
}
func otpSubject(subject, purpose string) string { return tokenHash(subject + "|" + purpose) }
func (r *Repository) SaveOTP(ctx context.Context, subject, purpose, digest string, expires time.Time) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO otp_challenges(subject_hash,purpose,code_hash,expires_at) VALUES($1,$2,$3,$4) ON CONFLICT(subject_hash) DO UPDATE SET code_hash=EXCLUDED.code_hash,attempts=0,expires_at=EXCLUDED.expires_at,created_at=now()`, otpSubject(subject, purpose), purpose, digest, expires)
	return err
}
func (r *Repository) ValidateOTP(ctx context.Context, subject, purpose, digest string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM otp_challenges WHERE subject_hash=$1 AND purpose=$2 AND code_hash=$3 AND expires_at>now()`, otpSubject(subject, purpose), purpose, digest)
	if err != nil || tag.RowsAffected() == 1 {
		return tag.RowsAffected() == 1, err
	}
	_, err = r.pool.Exec(ctx, `UPDATE otp_challenges SET attempts=attempts+1, expires_at=CASE WHEN attempts+1 >= 5 THEN now() ELSE expires_at END WHERE subject_hash=$1 AND purpose=$2 AND expires_at>now()`, otpSubject(subject, purpose), purpose)
	return false, err
}
func (r *Repository) SaveSession(ctx context.Context, raw, phone string, expires time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, err := ensureUser(ctx, tx, phone)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, tokenHash(raw), id, expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) LoadSession(ctx context.Context, raw string) (string, time.Time, error) {
	var phone string
	var expires time.Time
	err := r.pool.QueryRow(ctx, `SELECT u.phone,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now()`, tokenHash(raw)).Scan(&phone, &expires)
	return phone, expires, err
}
func (r *Repository) RevokeSession(ctx context.Context, raw string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash(raw))
	return err
}
func (r *Repository) RevokeUserSessions(ctx context.Context, phone string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions s SET revoked_at=now() FROM users u WHERE s.user_id=u.id AND u.phone=$1 AND s.revoked_at IS NULL`, phone)
	return err
}

func ensureUser(ctx context.Context, tx pgx.Tx, phone string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO users(phone,identity_provider) VALUES($1,'sms') ON CONFLICT(phone) DO UPDATE SET phone=EXCLUDED.phone RETURNING id`, phone).Scan(&id)
	return id, err
}
func insertAudit(ctx context.Context, tx pgx.Tx, e domain.AuditEvent) error {
	var actorID any
	if e.ActorID != "" && e.ActorID != "contractor" {
		id, err := ensureUser(ctx, tx, e.ActorID)
		if err != nil {
			return err
		}
		actorID = id
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,document_id,actor_id,action,payload_sha256,previous_hash,event_hash,occurred_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8)`, e.ID, e.DocumentID, actorID, e.Action, e.PayloadHash, e.PreviousHash, e.Hash, e.OccurredAt)
	return err
}
func insertOutboxAndCommit(ctx context.Context, tx pgx.Tx, aggregateID, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,type,payload) VALUES($1,$2,$3)`, aggregateID, kind, raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
