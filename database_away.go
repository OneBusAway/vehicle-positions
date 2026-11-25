package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// databaseAway reports whether err says the database can't answer for now, as
// opposed to having refused what it was asked, which it would refuse again: it
// could not be reached, dropped the connection, is starting or stopping, has no
// room, or took too long. A request that failed so can be made again later.
func databaseAway(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"), // connection exception
			strings.HasPrefix(pgErr.Code, "53"), // insufficient resources
			strings.HasPrefix(pgErr.Code, "57"), // operator intervention
			strings.HasPrefix(pgErr.Code, "58"), // system error
			strings.HasPrefix(pgErr.Code, "40"): // rolled back, such as a deadlock
			return true
		}
		return false
	}
	// A deadline that passes is a net.Error too.
	var connectErr *pgconn.ConnectError
	var netErr net.Error
	return errors.As(err, &connectErr) || errors.As(err, &netErr) || pgconn.SafeToRetry(err) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// storeErrorStatus is the status for a request the store failed. It is 503 while
// the database is away: the driver apps keep a report that was answered so and
// send it later, where they drop one the server can't handle (500), which it
// would not handle the next time either.
func storeErrorStatus(err error) int {
	if databaseAway(err) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}
