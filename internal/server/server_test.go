package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewValidatesOutputPathAndPort(t *testing.T) {
	t.Parallel()

	validOutputPath := t.TempDir()
	filePath := filepath.Join(t.TempDir(), "site.txt")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
	}

	tests := []struct {
		name       string
		outputPath string
		port       int
		wantErr    string
	}{
		{
			name:       "missing output path",
			outputPath: filepath.Join(t.TempDir(), "missing"),
			port:       DefaultPort,
			wantErr:    "does not exist",
		},
		{
			name:       "output path is not a directory",
			outputPath: filePath,
			port:       DefaultPort,
			wantErr:    "is not a directory",
		},
		{
			name:       "invalid negative port",
			outputPath: validOutputPath,
			port:       -1,
			wantErr:    "port must be between 1 and 65535",
		},
		{
			name:       "invalid high port",
			outputPath: validOutputPath,
			port:       65536,
			wantErr:    "port must be between 1 and 65535",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(tt.outputPath, tt.port)
			if err == nil {
				t.Fatal("New() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New() error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewDefaultsZeroPortToDefaultPort(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()

	srv, err := New(outputPath, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if srv.port != DefaultPort {
		t.Fatalf("New().port = %d, want %d", srv.port, DefaultPort)
	}
	if got := srv.Addr(); got != fmt.Sprintf(":%d", DefaultPort) {
		t.Fatalf("New().Addr() = %q, want %q", got, fmt.Sprintf(":%d", DefaultPort))
	}
}

func TestServerServesCleanURLsAndCustom404(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "index.html", "<html><body>home</body></html>")
	writeServerTestFile(t, outputPath, "alpha/index.html", "<html><body>alpha page</body></html>")
	writeServerTestFile(t, outputPath, "404.html", "<html><body>custom missing page</body></html>")

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	t.Run("slash form serves index file", func(t *testing.T) {
		t.Parallel()

		resp, err := ts.Client().Get(ts.URL + "/alpha/")
		if err != nil {
			t.Fatalf("GET /alpha/ error = %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /alpha/ status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		body := readServerResponseBody(t, resp)
		if !strings.Contains(body, "alpha page") {
			t.Fatalf("GET /alpha/ body = %q, want alpha page content", body)
		}
	})

	t.Run("non-slash form redirects to clean URL", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{
			Transport: ts.Client().Transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resp, err := client.Get(ts.URL + "/alpha")
		if err != nil {
			t.Fatalf("GET /alpha error = %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("GET /alpha status = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
		}
		if location := resp.Header.Get("Location"); location != "/alpha/" {
			t.Fatalf("GET /alpha Location = %q, want %q", location, "/alpha/")
		}
	})

	t.Run("missing path serves custom 404 page", func(t *testing.T) {
		t.Parallel()

		resp, err := ts.Client().Get(ts.URL + "/missing/path")
		if err != nil {
			t.Fatalf("GET /missing/path error = %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /missing/path status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}

		body := readServerResponseBody(t, resp)
		if !strings.Contains(body, "custom missing page") {
			t.Fatalf("GET /missing/path body = %q, want custom 404 content", body)
		}
	})
}

func TestServerNestedMissingPathUsesSiteRootForFallbackLinks(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "index.html", "<html><body>home</body></html>")
	writeServerTestFile(t, outputPath, "404.html", `<!doctype html>
<html>
<head>
  <link id="preview-style" rel="stylesheet" href="./style.css">
</head>
<body>
  <a id="home-link" href="./">Return home</a>
</body>
</html>`)

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/missing/path", nil)
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, req)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ServeHTTP(/missing/path) status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	body := readServerResponseBody(t, resp)
	baseHref := mustMatchServerTestAttribute(t, body, `<base[^>]*href="([^"]+)"`)
	styleHref := mustMatchServerTestAttribute(t, body, `<link[^>]*id="preview-style"[^>]*href="([^"]+)"`)
	homeHref := mustMatchServerTestAttribute(t, body, `<a[^>]*id="home-link"[^>]*href="([^"]+)"`)

	if got := resolveServerPreviewHref(t, "/missing/path", baseHref, styleHref); got != "/style.css" {
		t.Fatalf("stylesheet path = %q, want %q", got, "/style.css")
	}
	if got := resolveServerPreviewHref(t, "/missing/path", baseHref, homeHref); got != "/" {
		t.Fatalf("home link path = %q, want %q", got, "/")
	}
}

func TestServerNestedMissingPathResolvesPopoverPayloadFromInjectedBase(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "index.html", "<html><body>home</body></html>")
	writeServerTestFile(t, outputPath, "404.html", `<!doctype html>
<html>
<head>
  <title>Missing</title>
</head>
<body>
  <aside data-popover-card data-popover-root="./_popover/"></aside>
  <a id="preview-link" href="./alpha/" data-popover-path="alpha">Alpha</a>
</body>
</html>`)

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/missing/path", nil)
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, req)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ServeHTTP(/missing/path) status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	body := readServerResponseBody(t, resp)
	baseHref := mustMatchServerTestAttribute(t, body, `<base[^>]*href="([^"]+)"`)
	popoverRoot := mustMatchServerTestAttribute(t, body, `<aside[^>]*data-popover-root="([^"]+)"`)
	notePath := mustMatchServerTestAttribute(t, body, `<a[^>]*data-popover-path="([^"]+)"`)

	if got := resolveServerPreviewHref(t, "/missing/path", baseHref, popoverRoot+notePath+".json"); got != "/_popover/alpha.json" {
		t.Fatalf("popover payload path = %q, want %q", got, "/_popover/alpha.json")
	}
}

func TestServerReturnsDefault404WhenFallbackPageIsMissing(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "index.html", "<html><body>home</body></html>")

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, req)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ServeHTTP(/missing) status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	body := readServerResponseBody(t, resp)
	if body != "404 page not found\n" {
		t.Fatalf("ServeHTTP(/missing) body = %q, want default http.NotFound response", body)
	}
}

func TestServerStaticModeDoesNotInjectLiveReloadOrExposeEndpoint(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "alpha/index.html", "<html><body>alpha page</body></html>")

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	htmlResp, err := ts.Client().Get(ts.URL + "/alpha/")
	if err != nil {
		t.Fatalf("GET /alpha/ error = %v", err)
	}
	defer htmlResp.Body.Close()

	htmlBody := readServerResponseBody(t, htmlResp)
	if strings.Contains(htmlBody, "data-obsite-livereload") {
		t.Fatalf("GET /alpha/ body = %q, do not want livereload injection in static mode", htmlBody)
	}

	liveReloadResp, err := ts.Client().Get(ts.URL + "/_livereload")
	if err != nil {
		t.Fatalf("GET /_livereload error = %v", err)
	}
	defer liveReloadResp.Body.Close()

	if liveReloadResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /_livereload status = %d, want %d in static mode", liveReloadResp.StatusCode, http.StatusNotFound)
	}
	if contentType := liveReloadResp.Header.Get("Content-Type"); strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("GET /_livereload Content-Type = %q, do not want SSE endpoint in static mode", contentType)
	}
}

func TestServerInjectsLiveReloadScriptIntoHTMLResponsesOnly(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "index.html", "<html><body>home</body></html>")
	writeServerTestFile(t, outputPath, "alpha/index.html", "<html><body>alpha page</body></html>")
	writeServerTestFile(t, outputPath, "style.css", "body { color: black; }")

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv.EnableLiveReload()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	htmlResp, err := ts.Client().Get(ts.URL + "/alpha/")
	if err != nil {
		t.Fatalf("GET /alpha/ error = %v", err)
	}
	defer htmlResp.Body.Close()

	htmlBody := readServerResponseBody(t, htmlResp)
	if !strings.Contains(htmlBody, "data-obsite-livereload") {
		t.Fatalf("GET /alpha/ body = %q, want livereload script injection", htmlBody)
	}
	if acceptRanges := htmlResp.Header.Get("Accept-Ranges"); acceptRanges != "" {
		t.Fatalf("GET /alpha/ Accept-Ranges = %q, want empty after HTML rewrite", acceptRanges)
	}
	if contentRange := htmlResp.Header.Get("Content-Range"); contentRange != "" {
		t.Fatalf("GET /alpha/ Content-Range = %q, want empty after HTML rewrite", contentRange)
	}
	onDiskHTML, err := os.ReadFile(filepath.Join(outputPath, "alpha", "index.html"))
	if err != nil {
		t.Fatalf("os.ReadFile(index.html) error = %v", err)
	}
	if strings.Contains(string(onDiskHTML), "data-obsite-livereload") {
		t.Fatalf("served file on disk = %q, do not want injected livereload script persisted", string(onDiskHTML))
	}

	cssResp, err := ts.Client().Get(ts.URL + "/style.css")
	if err != nil {
		t.Fatalf("GET /style.css error = %v", err)
	}
	defer cssResp.Body.Close()

	cssBody := readServerResponseBody(t, cssResp)
	if strings.Contains(cssBody, "data-obsite-livereload") {
		t.Fatalf("GET /style.css body = %q, do not want livereload script in non-HTML response", cssBody)
	}
}

func TestServerInjectsLiveReloadScriptWhenPageMentionsMarkerText(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "alpha/index.html", `<html><body><p>Example marker: data-obsite-livereload</p></body></html>`)

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv.EnableLiveReload()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/alpha/")
	if err != nil {
		t.Fatalf("GET /alpha/ error = %v", err)
	}
	defer resp.Body.Close()

	body := readServerResponseBody(t, resp)
	if !strings.Contains(body, `<p>Example marker: data-obsite-livereload</p>`) {
		t.Fatalf("GET /alpha/ body = %q, want original marker text preserved", body)
	}
	if !strings.Contains(body, `<script data-obsite-livereload>`) {
		t.Fatalf("GET /alpha/ body = %q, want injected live-reload script", body)
	}
	if got := strings.Count(body, "data-obsite-livereload"); got != 2 {
		t.Fatalf("GET /alpha/ marker occurrence count = %d, want %d after preserving text and injecting script", got, 2)
	}
}

func TestServerNonHTMLGetBypassesInjectedResponseBuffering(t *testing.T) {
	t.Parallel()

	srv := &Server{
		fileServer: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, buffered := w.(*bufferedResponseWriter); buffered {
				t.Fatal("serveOutput() wrapped non-HTML GET in bufferedResponseWriter")
			}

			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Accept-Ranges", "bytes")
			_, err := w.Write([]byte("png"))
			if err != nil {
				t.Fatalf("Write() error = %v", err)
			}
		}),
	}
	srv.EnableLiveReload()

	req := httptest.NewRequest(http.MethodGet, "/image.png", nil)
	recorder := httptest.NewRecorder()

	srv.serveOutput(recorder, req, "/image.png")

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serveOutput(/image.png) status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("serveOutput(/image.png) Content-Type = %q, want %q", contentType, "image/png")
	}
	if acceptRanges := resp.Header.Get("Accept-Ranges"); acceptRanges != "bytes" {
		t.Fatalf("serveOutput(/image.png) Accept-Ranges = %q, want %q", acceptRanges, "bytes")
	}
	if body := readServerResponseBody(t, resp); body != "png" {
		t.Fatalf("serveOutput(/image.png) body = %q, want %q", body, "png")
	}
}

func TestServerRangeRequestsBypassHTMLInjection(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	html := "<html><body>alpha page</body></html>"
	writeServerTestFile(t, outputPath, "alpha/index.html", html)

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv.EnableLiveReload()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alpha/index.html", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Range", "bytes=0-15")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do(range request) error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range GET /alpha/index.html status = %d, want %d", resp.StatusCode, http.StatusPartialContent)
	}
	if contentRange := resp.Header.Get("Content-Range"); !strings.HasPrefix(contentRange, "bytes 0-15/") {
		t.Fatalf("range GET /alpha/index.html Content-Range = %q, want bytes 0-15/*", contentRange)
	}
	if acceptRanges := resp.Header.Get("Accept-Ranges"); acceptRanges == "" {
		t.Fatalf("range GET /alpha/index.html Accept-Ranges = %q, want non-empty passthrough header", acceptRanges)
	}

	body := readServerResponseBody(t, resp)
	if strings.Contains(body, "data-obsite-livereload") {
		t.Fatalf("range GET /alpha/index.html body = %q, do not want livereload script injection", body)
	}
	if body != html[:16] {
		t.Fatalf("range GET /alpha/index.html body = %q, want %q", body, html[:16])
	}
}

func TestServerLiveReloadEndpointStreamsReloadEvent(t *testing.T) {
	t.Parallel()

	outputPath := t.TempDir()
	writeServerTestFile(t, outputPath, "index.html", "<html><body>home</body></html>")

	srv, err := New(outputPath, DefaultPort)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv.EnableLiveReload()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/_livereload")
	if err != nil {
		t.Fatalf("GET /_livereload error = %v", err)
	}
	defer resp.Body.Close()

	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(connected) error = %v", err)
	}
	if strings.TrimSpace(line) != ": connected" {
		t.Fatalf("first SSE line = %q, want %q", strings.TrimSpace(line), ": connected")
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("ReadString(blank separator) error = %v", err)
	}

	srv.NotifyReload()

	reloadLine := make(chan string, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				reloadLine <- "error: " + err.Error()
				return
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, ":") {
				continue
			}
			reloadLine <- trimmed
			return
		}
	}()

	select {
	case line := <-reloadLine:
		if line != "data: reload" {
			t.Fatalf("reload event line = %q, want %q", line, "data: reload")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reload SSE event")
	}
}

func writeServerTestFile(t *testing.T, root string, relPath string, contents string) {
	t.Helper()

	filePath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
	}
	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
	}
}

func readServerResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll(response body) error = %v", err)
	}

	return string(body)
}

func mustMatchServerTestAttribute(t *testing.T, body string, pattern string) string {
	t.Helper()

	matches := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatalf("response body did not match %q", pattern)
	}

	return matches[1]
}

func resolveServerPreviewHref(t *testing.T, requestPath string, baseHref string, rawHref string) string {
	t.Helper()

	documentURL := &url.URL{Scheme: "http", Host: "preview.test", Path: requestPath}
	baseURL := documentURL
	if strings.TrimSpace(baseHref) != "" {
		parsedBase, err := url.Parse(baseHref)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", baseHref, err)
		}
		baseURL = documentURL.ResolveReference(parsedBase)
	}

	parsedHref, err := url.Parse(rawHref)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", rawHref, err)
	}

	return baseURL.ResolveReference(parsedHref).EscapedPath()
}
