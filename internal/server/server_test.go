package server

import (
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
