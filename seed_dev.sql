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
