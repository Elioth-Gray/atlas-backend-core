-- Initial master data for the registration form. Fixed IDs keep the seed stable across
-- environments; change it through new migrations.
INSERT INTO shop_category (id, category) VALUES
    ('0b7c1e2a-0001-4000-8000-000000000001', 'Makanan & Minuman'),
    ('0b7c1e2a-0001-4000-8000-000000000002', 'Kafe & Kedai Kopi'),
    ('0b7c1e2a-0001-4000-8000-000000000003', 'Retail & Toko'),
    ('0b7c1e2a-0001-4000-8000-000000000004', 'Fashion'),
    ('0b7c1e2a-0001-4000-8000-000000000005', 'Kecantikan & Perawatan'),
    ('0b7c1e2a-0001-4000-8000-000000000006', 'Jasa'),
    ('0b7c1e2a-0001-4000-8000-000000000007', 'Lainnya')
ON CONFLICT (id) DO NOTHING;

INSERT INTO shop_scale (id, scale) VALUES
    ('0b7c1e2a-0002-4000-8000-000000000001', 'Mikro'),
    ('0b7c1e2a-0002-4000-8000-000000000002', 'Kecil'),
    ('0b7c1e2a-0002-4000-8000-000000000003', 'Menengah'),
    ('0b7c1e2a-0002-4000-8000-000000000004', 'Besar')
ON CONFLICT (id) DO NOTHING;
