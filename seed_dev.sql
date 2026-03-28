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

-- Seed a development API key for feed access
-- Raw key: dev-feed-key
INSERT INTO api_keys (name, key_hash, active)
VALUES (
    'Local Dev Feed Consumer',
    'ede86837a4b0c9da541548997b71fcbdd529c2fec605801694c704de466296e9',
    TRUE
)
ON CONFLICT (key_hash) DO NOTHING;
