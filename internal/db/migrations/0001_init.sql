CREATE TABLE accounts (
    id                BIGSERIAL PRIMARY KEY,
    email             TEXT        NOT NULL UNIQUE,
    password_hash     TEXT        NOT NULL,
    email_verified_at TIMESTAMPTZ,
    plan              TEXT        NOT NULL DEFAULT 'free',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash BYTEA       PRIMARY KEY,
    account_id BIGINT      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_account_id ON sessions (account_id);

CREATE TABLE email_tokens (
    token_hash BYTEA       PRIMARY KEY,
    account_id BIGINT      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    purpose    TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);

-- A deleted QR keeps its row (status 'deleted') so its short code is never reused.
CREATE TABLE qr_codes (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT      NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    code        TEXT        NOT NULL UNIQUE,
    name        TEXT        NOT NULL DEFAULT '',
    destination TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'blocked', 'deleted')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX qr_codes_account_id ON qr_codes (account_id);
CREATE INDEX qr_codes_updated_at ON qr_codes (updated_at);

CREATE TABLE destination_changes (
    id          BIGSERIAL PRIMARY KEY,
    qr_id       BIGINT      NOT NULL REFERENCES qr_codes (id) ON DELETE CASCADE,
    destination TEXT        NOT NULL,
    is_initial  BOOLEAN     NOT NULL DEFAULT false,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX destination_changes_qr_id ON destination_changes (qr_id, changed_at);

-- No IP and no cookie data: only date, country (derived upstream) and device type.
CREATE TABLE scans (
    id         BIGSERIAL PRIMARY KEY,
    qr_id      BIGINT      NOT NULL REFERENCES qr_codes (id) ON DELETE CASCADE,
    scanned_at TIMESTAMPTZ NOT NULL,
    country    TEXT        NOT NULL DEFAULT '',
    device     TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX scans_qr_id_scanned_at ON scans (qr_id, scanned_at);
