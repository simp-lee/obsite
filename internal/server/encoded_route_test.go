package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServerServesPercentEncodedGeneratedRoute(t *testing.T) {
	output := t.TempDir()
	page := filepath.Join(output, "Start%20Here", "index.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("encoded route"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := New(output, 0)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Start%20Here/", nil)
	srv.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != "encoded route" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}
