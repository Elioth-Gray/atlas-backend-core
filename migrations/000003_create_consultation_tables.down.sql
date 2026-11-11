DROP TABLE IF EXISTS business_memories;
ALTER TABLE IF EXISTS consultation_sessions
    DROP CONSTRAINT IF EXISTS consultation_sessions_final_solution_message_fkey;
DROP TABLE IF EXISTS consultation_messages;
DROP TABLE IF EXISTS consultation_sessions;
