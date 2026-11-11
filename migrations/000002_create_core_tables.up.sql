-- Core ATLAS schema: auth & shops (master data) and Path A (feedback -> opinions -> issues).
-- Table names follow the DBML (case-folded by PostgreSQL).

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TYPE shop_role_enum AS ENUM ('Owner', 'Employee');
CREATE TYPE sentiment_enum AS ENUM ('Negative', 'Positive', 'Neutral');
-- Only severe opinions are flagged; feedback_opinions.severity is NULL otherwise.
CREATE TYPE severity_enum AS ENUM ('Severe');

-- Users: extend the boilerplate table with the auth columns from the DBML.
-- password holds a bcrypt hash; refresh_token holds the SHA-256 of the current refresh token.
ALTER TABLE users
    ADD COLUMN phone_number         TEXT,
    ADD COLUMN password             TEXT,
    ADD COLUMN password_reset_token TEXT,
    ADD COLUMN refresh_token        TEXT,
    ADD COLUMN is_deleted           BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE shop_category (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    category   TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_deleted BOOLEAN     NOT NULL DEFAULT false
);

CREATE TABLE shop_scale (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    scale      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_deleted BOOLEAN     NOT NULL DEFAULT false
);

CREATE TABLE shops (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL,
    description      TEXT,
    website          TEXT,
    image            TEXT,
    shop_category_id UUID        NOT NULL REFERENCES shop_category (id),
    shop_scale_id    UUID        NOT NULL REFERENCES shop_scale (id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_deleted       BOOLEAN     NOT NULL DEFAULT false
);

CREATE TABLE shops_users (
    id         UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    role       shop_role_enum NOT NULL,
    user_id    UUID           NOT NULL REFERENCES users (id),
    shop_id    UUID           NOT NULL REFERENCES shops (id),
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    is_deleted BOOLEAN        NOT NULL DEFAULT false
);

-- One active membership per user and shop; used by every /owner authorization check.
CREATE UNIQUE INDEX shops_users_user_shop_active_key ON shops_users (user_id, shop_id) WHERE NOT is_deleted;
CREATE INDEX shops_users_shop_idx ON shops_users (shop_id);

CREATE TABLE feedbacks (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT,
    rating     NUMERIC(2,1) NOT NULL CHECK (rating BETWEEN 1.0 AND 5.0),
    feedback   TEXT         NOT NULL,
    image      TEXT,
    shop_id    UUID         NOT NULL REFERENCES shops (id),
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    is_deleted BOOLEAN      NOT NULL DEFAULT false
);

CREATE INDEX feedbacks_shop_created_idx ON feedbacks (shop_id, created_at DESC);

-- Issues intentionally have no shop_id, status, or solution column: ownership is derived
-- through feedback_opinions -> feedbacks.shop_id.
CREATE TABLE issues (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    title      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_deleted BOOLEAN     NOT NULL DEFAULT false
);

CREATE TABLE feedback_opinions (
    id            UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    opinion_text  TEXT           NOT NULL,
    evidence_text TEXT,
    sentiment     sentiment_enum NOT NULL,
    severity      severity_enum,
    feedback_id   UUID           NOT NULL REFERENCES feedbacks (id),
    issue_id      UUID           REFERENCES issues (id),
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ    NOT NULL DEFAULT now(),
    is_deleted    BOOLEAN        NOT NULL DEFAULT false
);

CREATE INDEX feedback_opinions_feedback_idx ON feedback_opinions (feedback_id);
CREATE INDEX feedback_opinions_issue_idx ON feedback_opinions (issue_id);

-- embedding has no fixed dimension so the embedding model can change without a migration.
CREATE TABLE opinion_vectors (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    embedding           vector      NOT NULL,
    embedding_model     TEXT        NOT NULL,
    feedback_opinion_id UUID        NOT NULL REFERENCES feedback_opinions (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_deleted          BOOLEAN     NOT NULL DEFAULT false,
    CONSTRAINT opinion_vectors_opinion_key UNIQUE (feedback_opinion_id)
);

-- Durable queue for the Path A worker: one job per feedback, retried with exponential
-- backoff. A PROCESSING job whose lease (locked_until) expired is picked up again, so a
-- crashed worker never loses a feedback.
CREATE TABLE feedback_jobs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    feedback_id  UUID        NOT NULL REFERENCES feedbacks (id),
    status       VARCHAR(20) NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING', 'PROCESSING', 'DONE', 'FAILED')),
    attempts     INTEGER     NOT NULL DEFAULT 0,
    next_run_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT feedback_jobs_feedback_key UNIQUE (feedback_id)
);

CREATE INDEX feedback_jobs_due_idx ON feedback_jobs (next_run_at) WHERE status IN ('PENDING', 'PROCESSING');
