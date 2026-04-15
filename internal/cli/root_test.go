package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
)

func TestExecuteShowsRootHelp(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), nil)
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}

	for _, want := range []string{"build", "serve", "init"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q\n%s", want, stdout)
		}
	}
}

func TestExecuteRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{"foo"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want unknown command error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if !strings.Contains(err.Error(), `unknown command "foo" for "obsite"`) {
		t.Fatalf("error = %q, want unknown command message", err.Error())
	}
}

func executeForTest(t *testing.T, deps commandDependencies, args []string) (stdout string, stderr string, err error) {
	t.Helper()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	err = executeWithDeps(args, deps, &stdoutBuf, &stderrBuf)

	return stdoutBuf.String(), stderrBuf.String(), err
}

func testCommandDependencies() commandDependencies {
	return commandDependencies{
		loadSiteInput: func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
			return internalbuild.SiteInput{}, fmt.Errorf("unexpected loadSiteInput call")
		},
		buildSiteWithOptions: func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
			return nil, fmt.Errorf("unexpected buildSiteWithOptions call")
		},
		newPreviewServer: func(outputPath string, port int) (previewServer, error) {
			return nil, fmt.Errorf("unexpected newPreviewServer call")
		},
		newFileWatcher: func() (fileWatcher, error) {
			return nil, fmt.Errorf("unexpected newFileWatcher call")
		},
	}
}
