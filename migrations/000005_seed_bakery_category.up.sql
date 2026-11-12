-- Adds the Bakery category and limits new and updated shops to it for now.
-- is_enabled hides a category from the registration form and rejects it on input without
-- deleting it; to open another category later: UPDATE shop_category SET is_enabled = true WHERE ...
ALTER TABLE shop_category ADD COLUMN is_enabled BOOLEAN NOT NULL DEFAULT true;

INSERT INTO shop_category (id, category) VALUES
    ('0b7c1e2a-0001-4000-8000-000000000008', 'Bakery')
ON CONFLICT (id) DO NOTHING;

UPDATE shop_category SET is_enabled = (id = '0b7c1e2a-0001-4000-8000-000000000008');
