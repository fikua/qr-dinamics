-- Append-only record of who accepted or withdrew what, and when (terms,
-- privacy policy, marketing). Kept as evidence; no IP or user agent stored.
CREATE TABLE consents (
    id         BIGSERIAL PRIMARY KEY,
    account_id BIGINT      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL CHECK (kind IN ('terms', 'privacy', 'marketing')),
    version    TEXT        NOT NULL,
    granted    BOOLEAN     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX consents_account_id ON consents (account_id, kind, created_at);

-- Deleting an account must not free its short codes for reuse: the QR rows
-- survive as anonymous tombstones (no owner, no name, no destination).
ALTER TABLE qr_codes ALTER COLUMN account_id DROP NOT NULL;
ALTER TABLE qr_codes DROP CONSTRAINT qr_codes_account_id_fkey;
ALTER TABLE qr_codes ADD CONSTRAINT qr_codes_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE SET NULL;
