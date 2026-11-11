-- Removes the seeded rows that no shop references.
DELETE FROM shop_scale
WHERE id::text LIKE '0b7c1e2a-0002-4000-8000-%'
  AND NOT EXISTS (SELECT 1 FROM shops WHERE shops.shop_scale_id = shop_scale.id);

DELETE FROM shop_category
WHERE id::text LIKE '0b7c1e2a-0001-4000-8000-%'
  AND NOT EXISTS (SELECT 1 FROM shops WHERE shops.shop_category_id = shop_category.id);
