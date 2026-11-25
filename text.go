package main

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// storableText reports whether Postgres can hold s as text: it refuses a NUL,
// and bytes that are not UTF-8, with an error.
func storableText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// refuseUnstorableQuery answers 400 to an admin request, to the API or a page,
// with a query value Postgres can't hold. Every search and filter is looked up
// as it was sent, so such a value would be a 500 and an error in the log.
func refuseUnstorableQuery(next http.Handler) http.Handler {
	const problem = "query parameters must be UTF-8 with no NUL characters"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api := strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
		if (api || strings.HasPrefix(r.URL.Path, "/admin/")) && !storableQuery(r.URL.Query()) {
			if api {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			} else {
				http.Error(w, problem, http.StatusBadRequest)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func storableQuery(query url.Values) bool {
	for _, values := range query {
		for _, v := range values {
			if !storableText(v) {
				return false
			}
		}
	}
	return true
}
