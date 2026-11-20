package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every response goes through requestLogger, and through feedHealth.count on
// the report and feed routes; a handler behind them can still move its write
// deadline.
func TestStatusRecorder_KeepsTheWriteDeadline(t *testing.T) {
	want := time.Now().Add(time.Minute)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, http.NewResponseController(w).SetWriteDeadline(want))
	})
	for name, h := range map[string]http.Handler{
		"requestLogger": requestLogger(handler),
		"feed health":   newFeedHealth(time.Now).count(healthReports)(handler),
	} {
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
		assert.Equal(t, want, w.deadline, "%s: the deadline reached the writer underneath", name)
	}
}

// An import confirm that takes longer than WRITE_TIMEOUT, served as the
// server serves it, behind requestLogger, still answers with its result: the
// drivers' passwords are on that page and nowhere else.
func TestImportConfirm_OutlastsTheWriteTimeout(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{delay: 300 * time.Millisecond})
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	srv := httptest.NewUnstartedServer(requestLogger(mux))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	form := signedForm("vehicles", vehicleRowsFile(2), time.Now())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/import/vehicles/confirm", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, "admin"))
	resp, err := srv.Client().Do(req)
	require.NoError(t, err, "the confirm's page reached the admin")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "<html")
}
