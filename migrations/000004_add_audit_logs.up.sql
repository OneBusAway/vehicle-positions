CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    user_id BIGINT NOT NULL,
    action TEXT NOT NULL,
    ip_address TEXT NOT NULL,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    details TEXT NOT NULL
);

COMMENT ON TABLE audit_logs IS 'Audit logs for admin actions.';