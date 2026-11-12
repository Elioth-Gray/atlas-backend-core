-- Index optimizations for consultation idempotency, archive lookups, and dashboard aggregations.

CREATE INDEX IF NOT EXISTS idx_consultation_sessions_shop_issue_archived
    ON consultation_sessions (shop_id, issue_id, archived_at DESC)
    WHERE status = 'ARCHIVED';

CREATE INDEX IF NOT EXISTS idx_consultation_sessions_processing_req
    ON consultation_sessions (processing_request_id)
    WHERE processing_request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_consultation_messages_ai_req_lookup
    ON consultation_messages (request_id)
    WHERE role = 'ATLAS_AI' AND request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_feedback_opinions_active_lookup
    ON feedback_opinions (feedback_id, issue_id, sentiment, severity)
    WHERE NOT is_deleted;
