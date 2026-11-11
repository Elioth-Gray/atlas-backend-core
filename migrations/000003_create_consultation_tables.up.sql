-- Guided AI consultation (Path B): 3 tables, 8 foreign keys.

CREATE TABLE consultation_sessions (
    id                        UUID        PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    shop_id                   UUID        NOT NULL REFERENCES shops (id),
    issue_id                  UUID        NOT NULL REFERENCES issues (id),
    started_by_user_id        UUID        NOT NULL REFERENCES users (id),
    status                    VARCHAR(25) NOT NULL DEFAULT 'COLLECTING_CONTEXT'
                              CHECK (status IN ('COLLECTING_CONTEXT', 'SOLUTION_READY', 'ARCHIVED')),
    -- Optimistic locking: every state change increments it.
    state_version             BIGINT      NOT NULL DEFAULT 0,
    question_count            SMALLINT    NOT NULL DEFAULT 0,
    asked_topics              TEXT[]      NOT NULL DEFAULT '{}',
    skipped_topics            TEXT[]      NOT NULL DEFAULT '{}',
    context_snapshot          JSONB       NOT NULL,
    -- Request currently calling the AI service; NULL when idle.
    processing_request_id     UUID,
    final_solution_message_id UUID,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at               TIMESTAMPTZ
);

CREATE INDEX consultation_sessions_issue_idx ON consultation_sessions (issue_id, created_at DESC);
CREATE INDEX consultation_sessions_shop_idx ON consultation_sessions (shop_id);

CREATE TABLE consultation_messages (
    id                UUID         PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    session_id        UUID         NOT NULL REFERENCES consultation_sessions (id),
    role              VARCHAR(15)  NOT NULL CHECK (role IN ('OWNER', 'ATLAS_AI')),
    message_type      VARCHAR(20)  NOT NULL CHECK (message_type IN ('TEXT', 'SOLUTION')),
    content           TEXT,
    solution_data     JSONB,
    revision          INTEGER,
    topic_key         VARCHAR(100),
    model_id          VARCHAR(100),
    prompt_version    VARCHAR(50),
    memory_candidates JSONB        NOT NULL DEFAULT '[]',
    request_id        UUID,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT consultation_messages_payload_check CHECK (
        (message_type = 'TEXT' AND content IS NOT NULL)
        OR (message_type = 'SOLUTION' AND solution_data IS NOT NULL AND revision IS NOT NULL)
    )
);

CREATE INDEX idx_consultation_messages_session_order ON consultation_messages (session_id, created_at, id);
-- Idempotency: one AI response per (session, request_id).
CREATE UNIQUE INDEX consultation_messages_ai_request_key
    ON consultation_messages (session_id, request_id)
    WHERE role = 'ATLAS_AI' AND request_id IS NOT NULL;
-- Solution revisions are unique within a session.
CREATE UNIQUE INDEX consultation_messages_revision_key
    ON consultation_messages (session_id, revision)
    WHERE message_type = 'SOLUTION';

-- Added after consultation_messages exists (the two tables reference each other).
ALTER TABLE consultation_sessions
    ADD CONSTRAINT consultation_sessions_final_solution_message_fkey
    FOREIGN KEY (final_solution_message_id) REFERENCES consultation_messages (id);

CREATE TABLE business_memories (
    id                       UUID        PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    shop_id                  UUID        NOT NULL REFERENCES shops (id),
    fact_text                TEXT        NOT NULL,
    source_session_id        UUID        NOT NULL REFERENCES consultation_sessions (id),
    source_owner_message_ids JSONB       NOT NULL,
    confirmed_by_user_id     UUID        NOT NULL REFERENCES users (id),
    confirmed_at             TIMESTAMPTZ NOT NULL,
    is_active                BOOLEAN     NOT NULL DEFAULT true,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_business_memories_shop_active ON business_memories (shop_id, is_active);
