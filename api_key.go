package main

import "time"

type APIKey struct {
	ID         int64
	Name       string
	KeyHash    string
	Active     bool
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
