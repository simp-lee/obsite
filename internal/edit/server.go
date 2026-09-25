// Package edit provides the explicitly invoked, single-account editor server.
package edit

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	internalserver "github.com/simp-lee/obsite/internal/server"
	"golang.org/x/term"
)

const (
	sessionCookieName = "obsite_session"
	csrfHeaderName    = "X-Obsite-CSRF"
	controlPrefix     = "/_obsite/"
	sessionLifetime   = 12 * time.Hour
)

// Server combines the read-only generated-site server with the isolated edit
// control boundary. It never writes editor state into generated output.
type Server struct {
	static   *internalserver.Server
	port     int
	username string
	hash     string

	mu       sync.Mutex
	sessions map[string]session
}

var setupMu sync.Mutex

type session struct {
	username string
	csrf     string
	expires  time.Time
}

// New validates the configured account and generated output before returning a
// server. It does not listen or modify the vault.
func New(vaultPath, outputPath string, port int) (*Server, error) {
	cfg, err := internalconfig.LoadForBuildWithOutput(vaultPath, outputPath)
	if err != nil {
		return nil, err
	}
	if cfg.Edit == nil {
		return nil, fmt.Errorf("edit.username and edit.passwordHash must be configured before edit can listen")
	}
	static, err := internalserver.New(outputPath, port)
	if err != nil {
		return nil, err
	}
	return &Server{
		static:   static,
		port:     port,
		username: cfg.Edit.Username,
		hash:     cfg.Edit.PasswordHash,
		sessions: make(map[string]session),
	}, nil
}

// Handler exposes the HTTP handler for httptest and embedding.
func (s *Server) Handler() http.Handler { return s }

// ListenAndServe starts the editor server after construction and validation.
func (s *Server) ListenAndServe() error {
	if s == nil || s.static == nil {
		return fmt.Errorf("edit server is nil")
	}
	return http.ListenAndServe(s.static.Addr(), s)
}

// EnableLiveReload enables the existing static-server reload channel.
func (s *Server) EnableLiveReload() {
	if s != nil && s.static != nil {
		s.static.EnableLiveReload()
	}
}

// NotifyReload broadcasts a reload to connected public pages.
func (s *Server) NotifyReload() {
	if s != nil && s.static != nil {
		s.static.NotifyReload()
	}
}

// ServeHTTP keeps all editor control routes under the reserved prefix and
// delegates every other request to the ordinary read-only output server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "edit server is unavailable", http.StatusInternalServerError)
		return
	}
	if strings.HasPrefix(r.URL.Path, controlPrefix) {
		s.serveControl(w, r)
		return
	}
	if r.URL.Path == strings.TrimSuffix(controlPrefix, "/") {
		http.NotFound(w, r)
		return
	}
	s.static.ServeHTTP(w, r)
}

func (s *Server) serveControl(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, controlPrefix)
	switch path {
	case "login":
		s.serveLogin(w, r)
	case "logout":
		s.serveLogout(w, r)
	case "session":
		s.serveSession(w, r)
	case "csrf":
		s.serveCSRF(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		htmlBody := `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Obsite login</title></head><body><main><h1>Obsite login</h1><form method="post" action="/_obsite/login"><label>Username <input name="username" autocomplete="username"></label><label>Password <input type="password" name="password" autocomplete="current-password"></label><button type="submit">Log in</button></form></main></body></html>`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, htmlBody)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid login request", http.StatusBadRequest)
			return
		}
		username := r.Form.Get("username")
		password := r.Form.Get("password")
		validUsername := subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) == 1
		validPassword, err := internalconfig.VerifyArgon2idPassword(password, s.hash)
		if err != nil {
			validPassword = false
		}
		if !validUsername || !validPassword {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		sessionID, err := newToken()
		if err != nil {
			http.Error(w, "could not create session", http.StatusInternalServerError)
			return
		}
		csrf, err := newToken()
		if err != nil {
			http.Error(w, "could not create session", http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.sessions[sessionID] = session{username: s.username, csrf: csrf, expires: time.Now().Add(sessionLifetime)}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: sessionID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLifetime / time.Second)})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"authenticated":true}`)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess, ok := s.sessionForRequest(r)
	response := map[string]any{"authenticated": ok}
	if ok {
		response["username"] = sess.username
		response["csrf"] = sess.csrf
	}
	writeJSON(w, response)
}

func (s *Server) serveCSRF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess, ok := s.sessionForRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]string{"csrf": sess.csrf})
}

func (s *Server) serveLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID, sess, ok := s.sessionForRequestWithID(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !sameOrigin(r) || !validCSRF(r, sess.csrf) {
		http.Error(w, "csrf validation failed", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, map[string]bool{"authenticated": false})
}

func (s *Server) sessionForRequest(r *http.Request) (session, bool) {
	_, value, ok := s.sessionForRequestWithID(r)
	return value, ok
}

func (s *Server) sessionForRequestWithID(r *http.Request) (string, session, bool) {
	if r == nil {
		return "", session{}, false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.sessions[cookie.Value]
	if !ok || (!value.expires.IsZero() && time.Now().After(value.expires)) {
		if ok {
			delete(s.sessions, cookie.Value)
		}
		return "", session{}, false
	}
	return cookie.Value, value, true
}

func validCSRF(r *http.Request, expected string) bool {
	got := r.Header.Get(csrfHeaderName)
	if got == "" {
		got = r.FormValue("csrf")
	}
	return expected != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func sameOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return false
	}
	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	return strings.EqualFold(u.Scheme, expectedScheme) && strings.EqualFold(u.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func newToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Setup interactively adds the one edit account to an already valid config.
// It refuses non-terminal input so credentials are never silently generated or
// accepted from a redirected command stream.
func Setup(vaultPath string, input io.Reader, output io.Writer) error {
	cfg, err := internalconfig.LoadForBuild(vaultPath)
	if err != nil {
		return err
	}
	if cfg.Edit != nil {
		return fmt.Errorf("edit account is already configured")
	}
	file, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return fmt.Errorf("edit setup requires an interactive terminal")
	}
	if output == nil {
		output = io.Discard
	}
	setupMu.Lock()
	defer setupMu.Unlock()
	resolvedVault, err := internalfsutil.ResolveVaultPath(vaultPath)
	if err != nil {
		return err
	}
	configPath := filepath.Join(resolvedVault, internalconfig.Filename)
	_, original, _, err := internalfsutil.ReadContainedRegularFile(resolvedVault, internalconfig.Filename)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	_, _ = io.WriteString(output, "Username: ")
	username, err := readTerminalLine(file)
	if err != nil {
		return fmt.Errorf("read username: %w", err)
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username must be non-empty")
	}
	_, _ = io.WriteString(output, "Password: ")
	password, err := term.ReadPassword(int(file.Fd()))
	_, _ = io.WriteString(output, "\n")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if len(password) == 0 {
		return fmt.Errorf("password must be non-empty")
	}
	hash, err := internalconfig.HashArgon2idPassword(string(password))
	if err != nil {
		return err
	}
	if err := internalconfig.ValidateArgon2idPasswordHash(hash); err != nil {
		return err
	}
	updated := appendEditConfig(original, username, hash)
	if _, err := internalconfig.ValidateConfigBytes(updated); err != nil {
		return fmt.Errorf("validate generated edit config: %w", err)
	}
	if err := atomicReplaceConfig(resolvedVault, configPath, original, updated); err != nil {
		return err
	}
	_, _ = io.WriteString(output, "Edit account configured.\n")
	return nil
}

func readTerminalLine(file *os.File) (string, error) {
	var data []byte
	one := []byte{0}
	for {
		n, err := file.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				return string(data), nil
			}
			if one[0] != '\r' {
				data = append(data, one[0])
			}
		}
		if err != nil {
			return "", err
		}
	}
}

func appendEditConfig(original []byte, username, hash string) []byte {
	updated := append([]byte(nil), original...)
	if len(updated) != 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, '\n')
	}
	updated = append(updated, []byte("edit:\n  username: "+fmt.Sprintf("%q", username)+"\n  passwordHash: "+hash+"\n")...)
	return updated
}

func atomicReplaceConfig(vaultRoot, configPath string, expected, updated []byte) error {
	_, current, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, internalconfig.Filename)
	if err != nil {
		return fmt.Errorf("recheck config before setup: %w", err)
	}
	if subtle.ConstantTimeCompare(current, expected) != 1 {
		return errors.New("config changed during edit setup; retry without overwriting the external change")
	}
	if _, _, err := internalfsutil.InspectContainedRegularFile(vaultRoot, configPath); err != nil {
		return fmt.Errorf("inspect config before setup: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(configPath), ".obsite-config-*")
	if err != nil {
		return fmt.Errorf("create atomic config file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set atomic config permissions: %w", err)
	}
	if _, err := temporary.Write(updated); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write atomic config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync atomic config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close atomic config: %w", err)
	}
	_, current, _, err = internalfsutil.ReadContainedRegularFile(vaultRoot, internalconfig.Filename)
	if err != nil {
		return fmt.Errorf("recheck config before commit: %w", err)
	}
	if subtle.ConstantTimeCompare(current, expected) != 1 {
		return errors.New("config changed during edit setup; retry without overwriting the external change")
	}
	if err := os.Rename(temporaryName, configPath); err != nil {
		return fmt.Errorf("replace config atomically: %w", err)
	}
	return nil
}
