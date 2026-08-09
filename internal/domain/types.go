package domain

import "time"

type Role string

const (
	RoleCustomer   Role = "customer"
	RoleContractor Role = "contractor"
)

type DocumentStatus string

const (
	StatusDraft        DocumentStatus = "draft"
	StatusSent         DocumentStatus = "sent"
	StatusPepSigned    DocumentStatus = "pep_signed"
	StatusAwaitingUKEP DocumentStatus = "awaiting_ukep"
	StatusCompleted    DocumentStatus = "completed"
)

type SignatureType string

const (
	PEP  SignatureType = "pep"
	UKEP SignatureType = "ukep"
)

type Document struct {
	ID, Title, Template, TemplateVersion, CustomerID, ContractorPhone, ContentHash, ObjectKey string
	AmountKopecks                                                                             int64
	Status                                                                                    DocumentStatus
	AgreementVersion                                                                          string
	CreatedAt                                                                                 time.Time
}
type Signature struct {
	DocumentID, UserID, EvidenceHash, ProviderReference string
	Type                                                SignatureType
	SignedAt                                            time.Time
}
type AuditEvent struct {
	ID, DocumentID, ActorID, Action, PayloadHash, PreviousHash, Hash string
	OccurredAt                                                       time.Time
}
