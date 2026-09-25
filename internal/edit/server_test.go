package edit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "github.com/simp-lee/obsite/internal/config"
)

func TestServerAuthenticatesBelowReservedControlBoundary(t *testing.T) {
	vault := t.TempDir()
	output := t.TempDir()
	hash, err := internalconfig.HashArgon2idPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	writeEditFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\nedit:\n  username: admin\n  passwordHash: "+hash+"\n")
	if err := os.WriteFile(filepath.Join(output, "index.html"), []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}

	server, err := New(vault, output, 0)
	if err != nil {
		t.Fatal(err)
	}
	listener := httptest.NewServer(server)
	defer listener.Close()
	client, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := listener.Client()
	httpClient.Jar = client

	response, err := httpClient.Get(listener.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "public" {
		t.Fatalf("anonymous public response = %d %q", response.StatusCode, body)
	}

	response, err = httpClient.Get(listener.URL + "/_obsite/session")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("anonymous session status = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	response, err = httpClient.PostForm(listener.URL+"/_obsite/login", map[string][]string{"username": {"admin"}, "password": {"secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("login response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v, want HttpOnly and SameSite=Lax", cookies)
	}
	_ = response.Body.Close()

	response, err = httpClient.Get(listener.URL + "/_obsite/session")
	if err != nil {
		t.Fatal(err)
	}
	sessionBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(sessionBody), `"authenticated":true`) || !strings.Contains(string(sessionBody), `"csrf":`) {
		t.Fatalf("session body = %s", sessionBody)
	}
	var sessionData struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(sessionBody, &sessionData); err != nil || sessionData.CSRF == "" {
		t.Fatalf("session JSON = %s; err=%v", sessionBody, err)
	}

	request, err := http.NewRequest(http.MethodPost, listener.URL+"/_obsite/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", listener.URL)
	response, err = httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without csrf status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
	_ = response.Body.Close()

	request, err = http.NewRequest(http.MethodPost, listener.URL+"/_obsite/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", listener.URL)
	request.Header.Set(csrfHeaderName, sessionData.CSRF)
	response, err = httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logout with csrf status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
}

func writeEditFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
