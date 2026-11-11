DROP TABLE IF EXISTS feedback_jobs;
DROP TABLE IF EXISTS opinion_vectors;
DROP TABLE IF EXISTS feedback_opinions;
DROP TABLE IF EXISTS issues;
DROP TABLE IF EXISTS feedbacks;
DROP TABLE IF EXISTS shops_users;
DROP TABLE IF EXISTS shops;
DROP TABLE IF EXISTS shop_scale;
DROP TABLE IF EXISTS shop_category;

ALTER TABLE users
    DROP COLUMN IF EXISTS is_deleted,
    DROP COLUMN IF EXISTS refresh_token,
    DROP COLUMN IF EXISTS password_reset_token,
    DROP COLUMN IF EXISTS password,
    DROP COLUMN IF EXISTS phone_number;

DROP TYPE IF EXISTS severity_enum;
DROP TYPE IF EXISTS sentiment_enum;
DROP TYPE IF EXISTS shop_role_enum;

-- The vector extension stays installed: other database objects may depend on it.
