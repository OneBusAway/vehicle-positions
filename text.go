package main

import (
	"fmt"
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

// validateStorable refuses value, a request's field called name, when Postgres
// can't hold it.
func validateStorable(name, value string) error {
	if !storableText(value) {
		return fmt.Errorf("%s has a character that can't be saved", name)
	}
	return nil
}

// refuseUnstorableURL answers 400 to an admin request, to the API or a page,
// with a query value Postgres can't hold, and 404 to a page whose path has one.
// Every search and filter, and the id in a page's path, is looked up as it was
// sent, so such a value would be a 500 and an error in the log. The API checks
// the ids in its own paths.
func refuseUnstorableURL(next http.Handler) http.Handler {
	const problem = "query parameters must be UTF-8 with no NUL characters"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api := strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
		if strings.HasPrefix(r.URL.Path, "/admin/") && !storableText(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
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
