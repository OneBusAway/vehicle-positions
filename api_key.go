package main

import "time"

// APIKey is an api_keys row. KeyHash is never serialized: the raw key is
// returned once at creation and is unrecoverable afterward, since only its
// hash is stored.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"`
	Active     bool       `json:"active"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
