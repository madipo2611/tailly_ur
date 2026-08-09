CREATE INDEX documents_customer_created_idx ON documents (customer_id, created_at DESC);
CREATE INDEX documents_contractor_created_idx ON documents (contractor_phone, created_at DESC);
CREATE INDEX documents_status_idx ON documents (status);
CREATE INDEX signatures_document_signed_idx ON signatures (document_id, signed_at);
CREATE INDEX audit_events_document_occurred_idx ON audit_events (document_id, occurred_at);
