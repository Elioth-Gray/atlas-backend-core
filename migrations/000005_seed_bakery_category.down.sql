-- Removes the Bakery category unless a shop uses it, and drops the enable flag.
DELETE FROM shop_category
WHERE id = '0b7c1e2a-0001-4000-8000-000000000008'
  AND NOT EXISTS (SELECT 1 FROM shops WHERE shops.shop_category_id = shop_category.id);

ALTER TABLE shop_category DROP COLUMN is_enabled;
