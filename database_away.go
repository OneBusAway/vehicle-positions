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
			strings.HasPrefix(pgErr.Code, "40"), // rolled back, such as a deadlock
			pgErr.Code == "55P03":               // a lock not had within lock_timeout
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

// writeReportFailure answers a report the store failed, with message when the
// server can't handle it. While the database is away it is a 503: the driver
// apps keep a report that was answered so and send it later, where they drop one
// answered 500, which the server would not handle the next time either.
func writeReportFailure(w http.ResponseWriter, err error, message string) {
	if databaseAway(err) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": message})
}
