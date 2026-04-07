package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeCommandRequiresOutputFlag(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{"serve"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want missing output flag error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if !strings.Contains(err.Error(), `required flag(s) "output" not set`) {
		t.Fatalf("error = %q, want required output flag message", err.Error())
	}
}

func TestServeCommandUsesServerDefaultPortWhenOmitted(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "site")
	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	var gotOutputPath string
	var gotPort int
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		gotOutputPath = outputPath
		gotPort = port
		return server, nil
	}

	_, _, err := executeForTest(t, deps, []string{"serve", "--output", outputPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if gotOutputPath != outputPath {
		t.Fatalf("newPreviewServer outputPath = %q, want %q", gotOutputPath, outputPath)
	}
	if gotPort != 0 {
		t.Fatalf("newPreviewServer port = %d, want 0 so server.New applies its default", gotPort)
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandPassesExplicitPortToPreviewServer(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "site")
	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	var gotOutputPath string
	var gotPort int
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		gotOutputPath = outputPath
		gotPort = port
		return server, nil
	}

	stdout, stderr, err := executeForTest(t, deps, []string{"serve", "--output", outputPath, "--port", "9090"})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if gotOutputPath != outputPath {
		t.Fatalf("newPreviewServer outputPath = %q, want %q", gotOutputPath, outputPath)
	}
	if gotPort != 9090 {
		t.Fatalf("newPreviewServer port = %d, want %d", gotPort, 9090)
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandPropagatesListenFailure(t *testing.T) {
	t.Parallel()

	deps := testCommandDependencies()
	server := &fakePreviewServer{listenErr: errors.New("bind failed")}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}

	_, _, err := executeForTest(t, deps, []string{"serve", "--output", filepath.Join(t.TempDir(), "site"), "--port", "9090"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want listen failure")
	}
	if !strings.Contains(err.Error(), "listen and serve: bind failed") {
		t.Fatalf("error = %q, want wrapped listen failure", err.Error())
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

type fakePreviewServer struct {
	listenErr   error
	listenCalls int
}

func (s *fakePreviewServer) ListenAndServe() error {
	s.listenCalls++
	return s.listenErr
}
