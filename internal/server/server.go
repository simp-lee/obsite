package server

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DefaultPort is the default local preview port for obsite serve.
const DefaultPort = 8080

// Server serves a generated Obsite output directory over HTTP.
type Server struct {
	outputPath   string
	port         int
	fileServer   http.Handler
	notFoundPath string
}

// New validates the generated output directory and constructs a preview server.
func New(outputPath string, port int) (*Server, error) {
	normalizedOutputPath, err := normalizeOutputPath(outputPath)
	if err != nil {
		return nil, err
	}
	normalizedPort := normalizePort(port)
	if err := validatePort(normalizedPort); err != nil {
		return nil, err
	}

	server := &Server{
		outputPath: normalizedOutputPath,
		port:       normalizedPort,
		fileServer: http.FileServer(http.Dir(normalizedOutputPath)),
	}

	notFoundPath := filepath.Join(normalizedOutputPath, "404.html")
	if info, err := os.Stat(notFoundPath); err == nil && !info.IsDir() {
		server.notFoundPath = notFoundPath
	}

	return server, nil
}

// Addr returns the listen address for the preview server.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}

	return fmt.Sprintf(":%d", s.port)
}

// ListenAndServe starts the preview server.
func (s *Server) ListenAndServe() error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}

	return http.ListenAndServe(s.Addr(), s)
}

// ServeHTTP handles clean-URL fallbacks before delegating to the underlying file server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "server is nil", http.StatusInternalServerError)
		return
	}

	servePath, redirectPath := s.resolvePath(r.URL.Path)
	if redirectPath != "" {
		if rawQuery := r.URL.RawQuery; rawQuery != "" {
			redirectPath += "?" + rawQuery
		}
		http.Redirect(w, r, redirectPath, http.StatusMovedPermanently)
		return
	}

	if servePath != "" {
		req := r.Clone(r.Context())
		req.URL.Path = servePath
		s.fileServer.ServeHTTP(w, req)
		return
	}

	if s.notFoundPath == "" {
		http.NotFound(w, r)
		return
	}

	s.serveNotFound(w, r)
}

func (s *Server) resolvePath(requestPath string) (servePath string, redirectPath string) {
	cleanPath, hasTrailingSlash := cleanRequestPath(requestPath)
	resolvedPath := filepath.Join(s.outputPath, filepath.FromSlash(strings.TrimPrefix(cleanPath, "/")))
	if cleanPath == "/" {
		resolvedPath = s.outputPath
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", ""
	}

	if info.IsDir() {
		if !hasIndexFile(resolvedPath) {
			return "", ""
		}
		if cleanPath != "/" && !hasTrailingSlash {
			return "", ensureTrailingSlash(cleanPath)
		}

		return ensureDirectoryPath(cleanPath), ""
	}

	if hasTrailingSlash && cleanPath != "/" {
		return "", cleanPath
	}

	return cleanPath, ""
}

func (s *Server) serveNotFound(w http.ResponseWriter, r *http.Request) {
	body, err := os.ReadFile(s.notFoundPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body = injectPreviewBaseHref(body)
	if len(body) == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusNotFound)
	if r.Method == http.MethodHead {
		return
	}

	_, _ = w.Write(body)
}

func normalizeOutputPath(outputPath string) (string, error) {
	trimmedPath := strings.TrimSpace(outputPath)
	if trimmedPath == "" {
		return "", fmt.Errorf("output path is required")
	}
	normalizedPath := filepath.Clean(trimmedPath)

	info, err := os.Stat(normalizedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("output path %q does not exist", normalizedPath)
		}

		return "", fmt.Errorf("stat output path %q: %w", normalizedPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("output path %q is not a directory", normalizedPath)
	}

	absPath, err := filepath.Abs(normalizedPath)
	if err != nil {
		return "", fmt.Errorf("resolve output path %q: %w", normalizedPath, err)
	}

	return absPath, nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	return nil
}

func normalizePort(port int) int {
	if port == 0 {
		return DefaultPort
	}

	return port
}

func injectPreviewBaseHref(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	lowerBody := bytes.ToLower(body)
	if bytes.Contains(lowerBody, []byte("<base")) {
		return body
	}

	headStart := bytes.Index(lowerBody, []byte("<head"))
	if headStart == -1 {
		return body
	}

	headEnd := bytes.IndexByte(lowerBody[headStart:], '>')
	if headEnd == -1 {
		return body
	}

	insertAt := headStart + headEnd + 1
	withBase := make([]byte, 0, len(body)+len("\n  <base href=\"/\">"))
	withBase = append(withBase, body[:insertAt]...)
	withBase = append(withBase, []byte("\n  <base href=\"/\">")...)
	withBase = append(withBase, body[insertAt:]...)

	return withBase
}

func cleanRequestPath(requestPath string) (cleanPath string, hasTrailingSlash bool) {
	if requestPath == "" {
		return "/", true
	}

	hasTrailingSlash = requestPath == "/" || strings.HasSuffix(requestPath, "/")
	cleanPath = path.Clean("/" + strings.TrimPrefix(requestPath, "/"))
	if cleanPath == "." {
		cleanPath = "/"
	}

	return cleanPath, hasTrailingSlash
}

func ensureDirectoryPath(cleanPath string) string {
	if cleanPath == "/" {
		return "/"
	}

	return ensureTrailingSlash(cleanPath)
}

func ensureTrailingSlash(path string) string {
	if path == "/" || strings.HasSuffix(path, "/") {
		return path
	}

	return path + "/"
}

func hasIndexFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}
