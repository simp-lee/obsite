package server

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// DefaultPort is the default local preview port for obsite serve.
const DefaultPort = 8080

const (
	liveReloadEndpoint   = "/_livereload"
	liveReloadQueryParam = "obsite-live-reload"
)

var liveReloadScript = []byte(`<script data-obsite-livereload>(function(){if(!window.EventSource){return;}var source=new EventSource("/_livereload?obsite-live-reload=1");source.onmessage=function(event){if(event.data==="reload"){source.close();window.location.reload();}};})();</script>`)

// Server serves a generated Obsite output directory over HTTP.
type Server struct {
	outputPath   string
	port         int
	fileServer   http.Handler
	notFoundPath string
	liveReload   *liveReloadHub
}

type liveReloadHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

type bufferedResponseWriter struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
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

// EnableLiveReload turns on the SSE endpoint and HTML script injection used by watch mode.
func (s *Server) EnableLiveReload() {
	if s == nil || s.liveReload != nil {
		return
	}

	s.liveReload = newLiveReloadHub()
}

// NotifyReload broadcasts a livereload event to connected preview clients.
func (s *Server) NotifyReload() {
	if s == nil || s.liveReload == nil {
		return
	}

	s.liveReload.Reload()
}

// ServeHTTP handles clean-URL fallbacks before delegating to the underlying file server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "server is nil", http.StatusInternalServerError)
		return
	}

	if cleanPath, _ := cleanRequestPath(r.URL.Path); cleanPath == liveReloadEndpoint && s.liveReload != nil && shouldServeLiveReloadRequest(r) {
		s.serveLiveReload(w, r)
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
		s.serveOutput(w, r, servePath)
		return
	}

	if s.notFoundPath == "" {
		http.NotFound(w, r)
		return
	}

	s.serveNotFound(w, r)
}

func (s *Server) resolvePath(requestPath string) (servePath string, redirectPath string) {
	if isUnsafePreviewRequestPath(requestPath) {
		return "", ""
	}

	cleanPath, _ := cleanRequestPath(requestPath)
	resolvedPath := filepath.Join(s.outputPath, filepath.FromSlash(strings.TrimPrefix(cleanPath, "/")))
	if cleanPath == "/" {
		resolvedPath = s.outputPath
	}
	if !pathWithinRoot(s.outputPath, resolvedPath) {
		return "", ""
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", ""
	}

	if info.IsDir() {
		if !hasIndexFile(resolvedPath) {
			return "", ""
		}

		canonicalPath := ensureDirectoryPath(cleanPath)
		if requestPath != canonicalPath {
			return "", canonicalPath
		}

		return canonicalPath, ""
	}

	if requestPath != cleanPath {
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
	if s.liveReload != nil {
		body = injectLiveReloadScript(body)
	}
	if len(body) == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusNotFound)
	if r.Method == http.MethodHead {
		return
	}

	_, _ = w.Write(body)
}

func (s *Server) serveLiveReload(w http.ResponseWriter, r *http.Request) {
	if s.liveReload == nil {
		http.Error(w, "live reload is unavailable", http.StatusServiceUnavailable)
		return
	}

	s.liveReload.ServeHTTP(w, r)
}

func shouldServeLiveReloadRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}

	if r.URL.Query().Get(liveReloadQueryParam) == "1" {
		return true
	}

	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	return strings.Contains(accept, "text/event-stream")
}

func (s *Server) serveOutput(w http.ResponseWriter, r *http.Request, servePath string) {
	req := r.Clone(r.Context())
	req.URL.Path = servePath
	if s.liveReload == nil || !shouldBufferInjectedResponse(req, servePath) {
		s.fileServer.ServeHTTP(w, req)
		return
	}

	s.serveInjectedResponse(w, req, func(recorder http.ResponseWriter, request *http.Request) {
		s.fileServer.ServeHTTP(recorder, request)
	})
}

func shouldBufferInjectedResponse(r *http.Request, servePath string) bool {
	if r == nil || r.Method != http.MethodGet || requestHasRange(r) {
		return false
	}

	return isHTMLCandidatePath(servePath)
}

func isHTMLCandidatePath(servePath string) bool {
	if servePath == "" {
		return false
	}
	if strings.HasSuffix(servePath, "/") {
		return true
	}

	return strings.EqualFold(path.Ext(servePath), ".html")
}

func (s *Server) serveInjectedResponse(w http.ResponseWriter, r *http.Request, serve func(http.ResponseWriter, *http.Request)) {
	recorder := newBufferedResponseWriter()
	serve(recorder, r)

	statusCode := recorder.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	body := recorder.body.Bytes()
	headers := cloneHeaders(recorder.Header())
	if statusCode != http.StatusPartialContent && !requestHasRange(r) && shouldInjectLiveReload(headers, body) {
		body = injectLiveReloadScript(body)
		headers.Set("Content-Length", strconv.Itoa(len(body)))
		clearRangeHeaders(headers)
	}

	copyHeaders(w.Header(), headers)
	w.WriteHeader(statusCode)
	if r.Method == http.MethodHead {
		return
	}

	_, _ = w.Write(body)
}

func requestHasRange(r *http.Request) bool {
	if r == nil {
		return false
	}

	return strings.TrimSpace(r.Header.Get("Range")) != ""
}

func clearRangeHeaders(headers http.Header) {
	if headers == nil {
		return
	}

	headers.Del("Accept-Ranges")
	headers.Del("Content-Range")
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

func isUnsafePreviewRequestPath(requestPath string) bool {
	normalized := normalizePreviewRequestPath(requestPath)
	if normalized == "" {
		return false
	}

	trimmed := strings.TrimLeft(normalized, "/")
	return hasWindowsDriveRequestPrefix(trimmed) || hasUNCRequestPrefix(normalized)
}

func normalizePreviewRequestPath(requestPath string) string {
	return strings.ReplaceAll(strings.TrimSpace(requestPath), `\`, "/")
}

func hasWindowsDriveRequestPrefix(value string) bool {
	if len(value) < 3 || value[1] != ':' {
		return false
	}

	first := value[0]
	if (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') {
		return false
	}

	return value[2] == '/'
}

func hasUNCRequestPrefix(value string) bool {
	if !strings.HasPrefix(value, "//") {
		return false
	}

	trimmed := strings.TrimLeft(value, "/")
	firstSep := strings.IndexByte(trimmed, '/')
	if firstSep <= 0 {
		return false
	}

	share := trimmed[firstSep+1:]
	if share == "" {
		return false
	}

	if secondSep := strings.IndexByte(share, '/'); secondSep >= 0 {
		return secondSep > 0
	}

	return true
}

func pathWithinRoot(root string, candidate string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(candidate) == "" {
		return false
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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

	insertAt, baseStart, baseEnd, hasBase, ok := previewBaseRewriteRange(body)
	if !ok {
		return body
	}

	if hasBase {
		withBase := make([]byte, 0, len(body)-(baseEnd-baseStart)+len(`<base href="/">`))
		withBase = append(withBase, body[:baseStart]...)
		withBase = append(withBase, []byte(`<base href="/">`)...)
		withBase = append(withBase, body[baseEnd:]...)

		return withBase
	}

	withBase := make([]byte, 0, len(body)+len("\n  <base href=\"/\">"))
	withBase = append(withBase, body[:insertAt]...)
	withBase = append(withBase, []byte("\n  <base href=\"/\">")...)
	withBase = append(withBase, body[insertAt:]...)

	return withBase
}

func previewBaseRewriteRange(body []byte) (insertAt int, baseStart int, baseEnd int, hasBase bool, ok bool) {
	tokenizer := xhtml.NewTokenizer(bytes.NewReader(body))
	offset := 0
	inHead := false
	implicitHead := false
	templateDepth := 0
	insertAt = -1

	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		start := offset
		end := start + len(raw)
		offset = end

		switch tokenType {
		case xhtml.ErrorToken:
			return insertAt, 0, 0, false, insertAt >= 0
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.DataAtom == atom.Html || strings.EqualFold(token.Data, "html") {
				if insertAt < 0 {
					insertAt = end
				}
				if !inHead {
					implicitHead = true
				}
				continue
			}
			if token.DataAtom == atom.Head || strings.EqualFold(token.Data, "head") {
				insertAt = end
				if tokenType == xhtml.SelfClosingTagToken {
					return insertAt, 0, 0, false, true
				}
				inHead = true
				implicitHead = false
				continue
			}
			if !inHead && !implicitHead {
				continue
			}

			if implicitHead && templateDepth == 0 && (token.DataAtom == atom.Body || strings.EqualFold(token.Data, "body")) {
				return insertAt, 0, 0, false, insertAt >= 0
			}
			if implicitHead && templateDepth == 0 && !isImplicitHeadElement(token) {
				if insertAt < 0 {
					insertAt = start
				}
				return insertAt, 0, 0, false, insertAt >= 0
			}

			if token.DataAtom == atom.Template || strings.EqualFold(token.Data, "template") {
				if tokenType != xhtml.SelfClosingTagToken {
					templateDepth++
				}
				continue
			}

			if templateDepth == 0 && (token.DataAtom == atom.Base || strings.EqualFold(token.Data, "base")) {
				return insertAt, start, end, true, true
			}
		case xhtml.EndTagToken:
			token := tokenizer.Token()
			if token.DataAtom == atom.Template || strings.EqualFold(token.Data, "template") {
				if templateDepth > 0 {
					templateDepth--
				}
				continue
			}

			if inHead && templateDepth == 0 && (token.DataAtom == atom.Head || strings.EqualFold(token.Data, "head")) {
				return insertAt, 0, 0, false, true
			}
			if implicitHead && templateDepth == 0 && (token.DataAtom == atom.Html || strings.EqualFold(token.Data, "html")) {
				return insertAt, 0, 0, false, insertAt >= 0
			}
		}
	}
}

func isImplicitHeadElement(token xhtml.Token) bool {
	name := strings.ToLower(strings.TrimSpace(token.Data))
	switch name {
	case "base", "basefont", "bgsound", "link", "meta", "noframes", "noscript", "script", "style", "template", "title":
		return true
	default:
		return false
	}
}

func injectLiveReloadScript(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	lowerBody := bytes.ToLower(body)
	if containsLiveReloadScriptTag(lowerBody) {
		return body
	}

	insertAt := bytes.LastIndex(lowerBody, []byte("</body>"))
	if insertAt == -1 {
		insertAt = bytes.LastIndex(lowerBody, []byte("</html>"))
	}
	if insertAt == -1 {
		insertAt = len(body)
	}

	withScript := make([]byte, 0, len(body)+len(liveReloadScript)+1)
	withScript = append(withScript, body[:insertAt]...)
	if insertAt > 0 && body[insertAt-1] != '\n' {
		withScript = append(withScript, '\n')
	}
	withScript = append(withScript, liveReloadScript...)
	if insertAt < len(body) {
		withScript = append(withScript, body[insertAt:]...)
	}

	return withScript
}

func containsLiveReloadScriptTag(lowerBody []byte) bool {
	const liveReloadAttribute = "data-obsite-livereload"

	searchStart := 0
	for {
		attributeOffset := bytes.Index(lowerBody[searchStart:], []byte(liveReloadAttribute))
		if attributeOffset == -1 {
			return false
		}
		attributeOffset += searchStart

		tagStart := bytes.LastIndexByte(lowerBody[:attributeOffset], '<')
		if tagStart == -1 {
			searchStart = attributeOffset + len(liveReloadAttribute)
			continue
		}
		if bytes.LastIndexByte(lowerBody[:attributeOffset], '>') > tagStart {
			searchStart = attributeOffset + len(liveReloadAttribute)
			continue
		}

		tagPrefix := bytes.TrimSpace(lowerBody[tagStart+1 : attributeOffset])
		if bytes.HasPrefix(tagPrefix, []byte("script")) {
			return true
		}

		searchStart = attributeOffset + len(liveReloadAttribute)
	}
}

func shouldInjectLiveReload(headers http.Header, body []byte) bool {
	if len(body) == 0 {
		return false
	}

	contentType := strings.ToLower(strings.TrimSpace(headers.Get("Content-Type")))
	if contentType == "" {
		contentType = strings.ToLower(http.DetectContentType(body))
	}

	return strings.HasPrefix(contentType, "text/html")
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

func newLiveReloadHub() *liveReloadHub {
	return &liveReloadHub{clients: make(map[chan string]struct{})}
}

func (h *liveReloadHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is unsupported", http.StatusInternalServerError)
		return
	}

	client := make(chan string, 1)
	h.register(client)
	defer h.unregister(client)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case message := <-client:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", message); err != nil {
				return
			}
			flusher.Flush()
		case <-pingTicker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *liveReloadHub) Reload() {
	h.broadcast("reload")
}

func (h *liveReloadHub) register(client chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = struct{}{}
}

func (h *liveReloadHub) unregister(client chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[client]; !ok {
		return
	}
	delete(h.clients, client)
	close(client)
}

func (h *liveReloadHub) broadcast(message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case client <- message:
		default:
		}
	}
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) Write(body []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(body)
}

func (w *bufferedResponseWriter) WriteHeader(statusCode int) {
	if w.statusCode != 0 {
		return
	}
	w.statusCode = statusCode
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func cloneHeaders(src http.Header) http.Header {
	if src == nil {
		return make(http.Header)
	}

	cloned := make(http.Header, len(src))
	copyHeaders(cloned, src)
	return cloned
}
