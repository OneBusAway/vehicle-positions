package main

import "time"

type AuditLogs struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Action    string    `json:"action"`
	IPAddress string    `json:"ip_address"`
	Timestamp time.Time `json:"timestamp"`
	Details   string    `json:"details"`
}
