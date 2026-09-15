-- Seed a test driver for local development
-- Email: driver@test.com  |  Password: password
INSERT INTO users (name, email, password_hash, role)
VALUES (
    'Test Driver',
    'driver@test.com',
    '$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi',
    'driver'
)
ON CONFLICT (email) DO NOTHING;

-- Seed a test admin for local development
-- Email: admin@test.com  |  Password: password
INSERT INTO users (name, email, password_hash, role)
VALUES (
    'Test Admin',
    'admin@test.com',
    '$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi',
    'admin'
)
ON CONFLICT (email) DO NOTHING;

-- Seed a feed API key for local development only. The raw key below is public
-- in this file, so it must never be used anywhere but a local machine; real
-- keys come from POST /api/v1/admin/api-keys and are shown only once.
-- Raw key: local-dev-feed-key
INSERT INTO api_keys (name, key_hash)
VALUES (
    'Local Development',
    '33975e14c74d0070a8fd7b1b52dd6dc01695edffaf5b91cf4cf190691b05c971'
)
ON CONFLICT (key_hash) DO NOTHING;
