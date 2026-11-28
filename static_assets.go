package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// staticPrefix is where the admin website's own files are mounted.
const staticPrefix = "/static/"

// staticAssets serves the admin website's own files: its stylesheet, scripts,
// icons and font. Embedded files have no modification time, so the standard
// file server sends them with nothing a browser can keep them by, and every
// page downloaded all of them again. Here each file is hashed and compressed
// once, at startup, and a page asks for it at an address that changes when
// any file does (see url), so a browser fetches it once and keeps it.
type staticAssets struct {
	// version is a hash of every file's name and contents.
	version string
	files   map[string]*staticFile
}

type staticFile struct {
	contentType string
	etag        string
	body        []byte
	// gzipped is nil for a file gzip barely shrinks: the font and the images
	// are compressed already.
	gzipped []byte
}

// embeddedStatic is the server's own static files, prepared once for the
// process: hashing and compressing them takes tens of milliseconds, which
// each admin UI built would otherwise pay again.
var embeddedStatic = sync.OnceValues(func() (*staticAssets, error) {
	return newStaticAssets(files, "web/static")
})

// newStaticAssets reads every file under root in fsys, and knows each by its
// path below root.
func newStaticAssets(fsys fs.FS, root string) (*staticAssets, error) {
	assets := &staticAssets{files: make(map[string]*staticFile)}
	set := sha256.New()
	// WalkDir visits names in lexical order, so the same files always hash to
	// the same version.
	err := fs.WalkDir(fsys, root, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(filePath, root+"/")
		sum := sha256.Sum256(body)
		fmt.Fprintf(set, "%s %x\n", name, sum)

		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = http.DetectContentType(body)
		}
		file := &staticFile{contentType: contentType, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, body: body}
		if gzipped := gzipBytes(body); len(gzipped) < len(body)*9/10 {
			file.gzipped = gzipped
		}
		assets.files[name] = file
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read static files: %w", err)
	}
	assets.version = hex.EncodeToString(set.Sum(nil)[:6])
	return assets, nil
}

func gzipBytes(body []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	// Neither can fail: the writer is a buffer in memory.
	_, _ = zw.Write(body)
	_ = zw.Close()
	return buf.Bytes()
}

// url is the address a page asks for the file name at, which the templates
// reach as {{asset "name"}}. The version is of the whole set, not of the one
// file, and sits in the path, not in a query: a file another names by a
// relative address (the font in the stylesheet, Leaflet's images in its own)
// then carries the version too, with neither file rewritten. The price is
// that a release that changes one file has browsers fetch them all again,
// once.
func (a *staticAssets) url(name string) (string, error) {
	if _, ok := a.files[name]; !ok {
		return "", fmt.Errorf("no static file %q", name)
	}
	return staticPrefix + a.version + "/" + name, nil
}

// ServeHTTP serves a file by its path under staticPrefix, with or without
// the version in front. At the version this server runs, the file can never
// change at that address, so a browser may keep it for a year without asking
// again. At any other address (no version, or the version of another release,
// which a page left open across an upgrade still asks for) the browser gets
// the current file and is told to ask each time whether it has changed; the
// ETag answers that with a 304.
func (a *staticAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	file, ok := a.files[name]
	versioned := false
	if !ok {
		var version string
		version, name, _ = strings.Cut(name, "/")
		file, ok = a.files[name]
		versioned = version == a.version
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Type", file.contentType)
	h.Set("ETag", file.etag)
	if versioned {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	body := file.body
	if file.gzipped != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			body = file.gzipped
			h.Set("Content-Encoding", "gzip")
			// The same file in other bytes, which a weak ETag says.
			h.Set("ETag", "W/"+file.etag)
			// ServeContent leaves the length out beside a Content-Encoding.
			h.Set("Content-Length", strconv.Itoa(len(body)))
		}
	}
	// ServeContent answers If-None-Match from the ETag set above.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
