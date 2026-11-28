package main

import (
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const keptForAYear = "public, max-age=31536000, immutable"

// embeddedAssets is the static file set the server builds from web/static.
func embeddedAssets(t *testing.T) *staticAssets {
	t.Helper()
	assets, err := embeddedStatic()
	require.NoError(t, err)
	return assets
}

// assetURL is the address a page asks for the static file name at.
func assetURL(t *testing.T, name string) string {
	t.Helper()
	u, err := embeddedAssets(t).url(name)
	require.NoError(t, err)
	return u
}

// embeddedStaticFiles is every file under web/static, by its path below it.
func embeddedStaticFiles(t *testing.T) map[string][]byte {
	t.Helper()
	found := make(map[string][]byte)
	require.NoError(t, fs.WalkDir(files, "web/static", func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(files, p)
		found[strings.TrimPrefix(p, "web/static/")] = body
		return err
	}))
	require.NotEmpty(t, found)
	return found
}

// request sends h one request with the given headers.
func request(h http.Handler, method, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// getPage sends h a GET signed in as an admin, or signed out for the sign-in
// page, which is for a visitor with no session.
func getPage(t *testing.T, h http.Handler, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if path != "/admin/login" {
		req.AddCookie(cookieFor(t, "admin"))
	}
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// newSiteHandler is the server's whole handler with the fixture schedule
// loaded, as main builds it.
func newSiteHandler(t *testing.T) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	catalog := testCatalog(t)
	reports := newServiceReporter(&fakeReportStore{}, catalog.index, testReportSettings)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, catalog, nil, newAlertWatcher(&noopStore{}, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	return h
}

func TestStaticAssets_AVersionedAddressIsKeptForAYear(t *testing.T) {
	h := newTestHandler(t, true)
	types := map[string]string{
		".css": "text/css", ".js": "text/javascript", ".svg": "image/svg+xml",
		".woff2": "font/woff2", ".png": "image/png", ".txt": "text/plain",
	}

	for name, body := range embeddedStaticFiles(t) {
		address := assetURL(t, name)
		require.Regexp(t, `^/static/[0-9a-f]{12}/`+regexp.QuoteMeta(name)+`$`, address)

		w := request(h, http.MethodGet, address)

		require.Equal(t, http.StatusOK, w.Code, name)
		assert.Equal(t, keptForAYear, w.Header().Get("Cache-Control"), name)
		assert.Regexp(t, `^"[0-9a-f]{16}"$`, w.Header().Get("ETag"), name)
		wantType, known := types[filepath.Ext(name)]
		require.True(t, known, "%s: a new kind of static file needs its Content-Type checked here", name)
		assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), wantType), "%s is sent as %s", name, w.Header().Get("Content-Type"))
		assert.Equal(t, strconv.Itoa(len(body)), w.Header().Get("Content-Length"), name)
		assert.True(t, string(body) == w.Body.String(), "%s is not the embedded file", name)
	}
}

// Without the version, or with another release's, the file is the current
// one and the browser is told to ask each time: only the address of this
// server's own version may be kept without asking.
func TestStaticAssets_AnyOtherAddressAsksEachTime(t *testing.T) {
	h := newTestHandler(t, true)
	script := embeddedStaticFiles(t)["js/app.js"]

	for _, address := range []string{"/static/js/app.js", "/static/0123456789ab/js/app.js"} {
		w := request(h, http.MethodGet, address)
		require.Equal(t, http.StatusOK, w.Code, address)
		assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"), address)
		// Not assert.Equal: on failure it prints the whole script.
		assert.True(t, string(script) == w.Body.String(), "%s is not the embedded script", address)
		etag := w.Header().Get("ETag")
		require.NotEmpty(t, etag, address)

		same := request(h, http.MethodGet, address, "If-None-Match", etag)
		assert.Equal(t, http.StatusNotModified, same.Code, address)
		assert.Empty(t, same.Body.String(), address)
		assert.Equal(t, etag, same.Header().Get("ETag"), address)
		assert.Equal(t, "no-cache", same.Header().Get("Cache-Control"), address)

		changed := request(h, http.MethodGet, address, "If-None-Match", `"0000000000000000"`)
		assert.Equal(t, http.StatusOK, changed.Code, address)
		assert.True(t, string(script) == changed.Body.String(), "%s is not the embedded script", address)
	}
}

// A folder is no file: the listing the standard file server gave is gone.
func TestStaticAssets_NotFound(t *testing.T) {
	h := newTestHandler(t, true)
	version := embeddedAssets(t).version

	for _, address := range []string{
		"/static/",
		"/static/js/",
		"/static/js",
		"/static/js/nope.js",
		"/static/" + version + "/",
		"/static/" + version + "/js/nope.js",
		"/static/" + version + "/" + version + "/js/app.js",
	} {
		w := request(h, http.MethodGet, address)
		assert.Equal(t, http.StatusNotFound, w.Code, address)
		assert.Empty(t, w.Header().Get("Cache-Control"), address)
		assert.Empty(t, w.Header().Get("ETag"), address)
	}
}

func TestNewStaticAssets_VersionFollowsTheFiles(t *testing.T) {
	text := strings.Repeat("body { color: red } ", 200)
	build := func(t *testing.T, fsys fstest.MapFS) *staticAssets {
		t.Helper()
		assets, err := newStaticAssets(fsys, "web/static")
		require.NoError(t, err)
		return assets
	}
	base := func() fstest.MapFS {
		return fstest.MapFS{
			"web/static/css/admin.css": {Data: []byte(text)},
			"web/static/js/app.js":     {Data: []byte("var a = 1;")},
			"web/templates/base.html":  {Data: []byte("not a static file")},
		}
	}

	first := build(t, base())
	require.Regexp(t, `^[0-9a-f]{12}$`, first.version)
	assert.Equal(t, first.version, build(t, base()).version, "the same files are the same version")
	assert.Len(t, first.files, 2, "only what is under the root")

	edited := base()
	edited["web/static/js/app.js"] = &fstest.MapFile{Data: []byte("var a = 2;")}
	second := build(t, edited)
	assert.NotEqual(t, first.version, second.version, "an edited file is a new version")
	assert.NotEqual(t, first.files["js/app.js"].etag, second.files["js/app.js"].etag)
	assert.Equal(t, first.files["css/admin.css"].etag, second.files["css/admin.css"].etag, "a file that did not change keeps its ETag")

	renamed := base()
	renamed["web/static/js/main.js"] = renamed["web/static/js/app.js"]
	delete(renamed, "web/static/js/app.js")
	assert.NotEqual(t, first.version, build(t, renamed).version, "a renamed file is a new version")

	added := base()
	added["web/static/js/line.js"] = &fstest.MapFile{Data: []byte("var b;")}
	assert.NotEqual(t, first.version, build(t, added).version, "an added file is a new version")
}

// The server image has no list of types by extension beyond Go's own few,
// which leaves out .woff2: the type is then read from the file itself.
func TestNewStaticAssets_ReadsATypeTheExtensionDoesNotGive(t *testing.T) {
	assets, err := newStaticAssets(fstest.MapFS{
		"static/admin.css":      {Data: []byte("body { color: red }")},
		"static/font.webfont-2": {Data: []byte("wOF2 and the rest of a font")},
	}, "static")
	require.NoError(t, err)

	require.NotNil(t, assets.files["admin.css"])
	require.NotNil(t, assets.files["font.webfont-2"])
	assert.Equal(t, "text/css; charset=utf-8", assets.files["admin.css"].contentType)
	assert.Equal(t, "font/woff2", assets.files["font.webfont-2"].contentType)
}

// unreadableFS lists its files and fails to read them.
type unreadableFS struct{ fstest.MapFS }

func (unreadableFS) ReadFile(string) ([]byte, error) { return nil, errors.New("disk gone") }

func TestNewStaticAssets_ReportsAFileItCannotRead(t *testing.T) {
	_, err := newStaticAssets(os.DirFS(t.TempDir()), "missing")
	require.ErrorContains(t, err, "read static files")
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = newStaticAssets(unreadableFS{fstest.MapFS{"static/app.js": {Data: []byte("var a;")}}}, "static")
	require.ErrorContains(t, err, "read static files: disk gone")
}

// A template that names a file the server does not have fails to render,
// rather than sending the browser to a 404.
func TestStaticAssets_ATemplateCannotNameAMissingFile(t *testing.T) {
	assets := embeddedAssets(t)
	_, err := assets.url("js/nope.js")
	require.ErrorContains(t, err, `no static file "js/nope.js"`)

	tmpl := template.Must(template.New("page").Funcs(template.FuncMap{"asset": assets.url}).Parse(`<script src="{{asset "js/nope.js"}}"></script>`))
	err = tmpl.Execute(io.Discard, nil)
	require.ErrorContains(t, err, `no static file "js/nope.js"`)
}

// Every template names its static files through asset, and each name is a
// file the server has. The site's own scripts and stylesheet name others by
// a relative address or one read from the page. A bare /static/ address
// would work, and be downloaded again on every page.
func TestStaticAssets_NothingNamesAFileByABareAddress(t *testing.T) {
	assets := embeddedAssets(t)
	named := regexp.MustCompile(`\{\{asset "([^"]+)"\}\}`)
	names := 0
	require.NoError(t, fs.WalkDir(files, "web/templates", func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(files, p)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "/static/", "%s names a static file without asset", p)
		for _, m := range named.FindAllStringSubmatch(string(body), -1) {
			names++
			_, err := assets.url(m[1])
			assert.NoError(t, err, "%s names %s", p, m[1])
		}
		return nil
	}))
	assert.Greater(t, names, 10, "the templates name their static files")

	for name, body := range embeddedStaticFiles(t) {
		if strings.HasPrefix(name, "vendor/") || strings.HasSuffix(name, ".txt") {
			continue
		}
		assert.False(t, strings.Contains(string(body), "/static/"), "%s names a static file by a bare address", name)
	}
}

// The three page skeletons (the shell, sign-in and the error page) each
// preload the font. The preload only helps if it names the very address the
// stylesheet's own reference to the font resolves to: at any other, the
// browser downloads the font twice.
func TestStaticAssets_ThePreloadIsTheFontTheStylesheetAsksFor(t *testing.T) {
	h := newSiteHandler(t)
	preload := regexp.MustCompile(`<link rel="preload" href="([^"]+)" as="font" type="font/woff2" crossorigin>`)
	stylesheet := regexp.MustCompile(`<link rel="stylesheet" href="([^"]+/css/admin\.css)">`)
	fontInCSS := regexp.MustCompile(`url\(([^)]+\.woff2)\)`)

	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/admin/more", http.StatusOK},
		{"/admin/login", http.StatusOK},
		{"/admin/no-such-page", http.StatusNotFound},
	} {
		page := getPage(t, h, tc.path)
		require.Equal(t, tc.status, page.Code, tc.path)
		html := page.Body.String()
		preloaded := preload.FindStringSubmatch(html)
		require.NotNil(t, preloaded, "%s preloads the font", tc.path)
		sheet := stylesheet.FindStringSubmatch(html)
		require.NotNil(t, sheet, "%s links the stylesheet", tc.path)

		css := request(h, http.MethodGet, sheet[1])
		require.Equal(t, http.StatusOK, css.Code)
		inCSS := fontInCSS.FindStringSubmatch(css.Body.String())
		require.NotNil(t, inCSS, "the stylesheet names its font")
		base, err := url.Parse(sheet[1])
		require.NoError(t, err)
		font, err := base.Parse(strings.Trim(inCSS[1], `"'`))
		require.NoError(t, err)
		assert.Equal(t, font.String(), preloaded[1], "%s preloads the address the stylesheet's font resolves to", tc.path)

		got := request(h, http.MethodGet, preloaded[1])
		assert.Equal(t, http.StatusOK, got.Code)
		assert.Equal(t, "font/woff2", got.Header().Get("Content-Type"))
		assert.Equal(t, keptForAYear, got.Header().Get("Cache-Control"), "the font is fetched once and kept")
	}
}
