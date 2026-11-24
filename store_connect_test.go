package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewStore_ErrorLeavesOutThePassword: the server logs why it could not use
// DATABASE_URL, and the log must not have any of the password in it. Every
// password here starts and ends in letters no other part of its URL has. No
// database is needed: nothing listens on port 1.
func TestNewStore_ErrorLeavesOutThePassword(t *testing.T) {
	const (
		notURL    = "connect to database: DATABASE_URL is not a valid URL; special characters in its user name and password must be percent-encoded, such as %2F for / and %40 for @"
		notScheme = "connect to database: DATABASE_URL must start with postgres:// or postgresql://"
	)
	tests := []struct {
		name string
		url  string
		// want is the whole error, or a part of it where the driver words it.
		want  string
		exact bool
	}{
		{"a slash in the password", "postgres://u:qqq/zzz@127.0.0.1:1/db", notURL, true},
		{"a question mark in the password", "postgres://u:qqq?zzz@127.0.0.1:1/db", notURL, true},
		{"a hash in the password", "postgres://u:qqq#zzz@127.0.0.1:1/db", notURL, true},
		{"a password that starts with a slash", "postgres://u:/qqqzzz@127.0.0.1:1/db", notURL, true},
		{"digits before a slash in the password", "postgres://u:123/qqqzzz@127.0.0.1:1/db", notURL, true},
		{"digits before a slash, and a bad sslmode", "postgres://u:123/qqqzzz@127.0.0.1:1/db?sslmode=bogus", notURL, true},
		{"digits before a question mark in the password", "postgres://u:123?qqqzzz@127.0.0.1:1/db", notURL, true},
		{"digits before a hash in the password", "postgres://u:123#qqqzzz@127.0.0.1:1/db", notURL, true},
		{"an at sign before a slash in the password", "postgres://u:qqq@zzz/qqq@127.0.0.1:1/db", notURL, true},
		{"a slash in the user name", "postgres://u/v:qqqzzz@127.0.0.1:1/db", notURL, true},
		{"an at sign in a query password", "postgres://127.0.0.1:1/db?password=qqq@zzz", notURL, true},
		{"a bracket in the password", "postgres://u:qqq[zzz@127.0.0.1:1/db", notURL, true},
		{"a space in the password", "postgres://u:qqq zzz@127.0.0.1:1/db", notURL, true},
		{"a bad escape in the password", "postgres://u:qqq%zzzz@127.0.0.1:1/db", notURL, true},
		{"a password in the query and a bad sslmode", "postgres://127.0.0.1:1/db?sslmode=bogus&password=qqqzzz", "sslmode is invalid", false},
		{"a password in the query and a bad port", "postgres://127.0.0.1:99999/db?password=qqqzzz", "invalid port", false},
		{"an escaped password in the query", "postgres://127.0.0.1:99999/db?password=qqq%2Fzzz", "invalid port", false},
		{"a password in the query under a name in capitals", "postgres://127.0.0.1:99999/db?Password=qqqzzz", "invalid port", false},
		{"an sslpassword", "postgres://u:qqqzzz@127.0.0.1:1/db?sslpassword=qqqzzz&pool_max_conns=x", "cannot parse pool_max_conns", false},
		{"a password and a bad sslmode", "postgresql://u:qqqzzz@127.0.0.1:1/db?sslmode=bogus", "sslmode is invalid", false},
		{"the key=value form", "host=127.0.0.1 port=1 user=u password=qqq:zzz", notScheme, true},
		{"the key=value form, cut short", "host=127.0.0.1 port=1 password='qqqzzz", notScheme, true},
		{"another scheme", "mysql://u:qqqzzz@127.0.0.1:1/db", notScheme, true},
		{"no scheme", "u:qqqzzz@127.0.0.1:1/db", notScheme, true},
		{"an at sign in the password", "postgres://u:qqq@zzz@127.0.0.1:1/db", "database=db", false},
		{"escaped at signs after the host", "postgres://127.0.0.1:1/d%40b?user=v%40w&password=qqq%40zzz", "`user=v@w database=d@b`", false},
		{"a database that isn't there", "postgresql://u:qqqzzz@127.0.0.1:1/db", "ping database: ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewStore(context.Background(), tt.url)
			require.Error(t, err)
			if tt.exact {
				assert.EqualError(t, err, tt.want)
			} else {
				assert.ErrorContains(t, err, tt.want, "the reason is kept")
			}
			assert.NotContains(t, err.Error(), "qqq")
			assert.NotContains(t, err.Error(), "zzz")
		})
	}
}
