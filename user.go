package main

import "time"

// User represents a row in the users table.
type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         string
	Active       bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
