package build

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/recommend"
	"github.com/simp-lee/obsite/internal/testutil/relatedfixture"
)

const relatedEndToEndRSSHelperEnv = "OBSITE_RELATED_END_TO_END_RSS_HELPER"

func TestRelatedPerformanceBudgets(t *testing.T) {
	if os.Getenv("OBSITE_RELATED_PERF") != "1" {
		t.Skip("set OBSITE_RELATED_PERF=1 on the fixed acceptance host")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"end-to-end", "rss"} {
		command := exec.Command(filepath.Join(root, "test", "verify-related-benchmarks.sh"), mode)
		command.Dir = root
		command.Env = os.Environ()
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s performance verification error = %v\n%s", mode, err, output)
		}
		t.Logf("%s", output)
	}
}

func TestRelatedMemoryLifetimes(t *testing.T) {
	if os.Getenv("OBSITE_RELATED_PROFILE_DIR") == "" {
		t.Skip("set OBSITE_RELATED_PROFILE_DIR to an external profile directory")
	}
	TestMemoryProfileCheckpoints(t)
}

func BenchmarkRelatedEndToEndWarm(b *testing.B) {
	b.Run("mixed-5000", func(b *testing.B) {
		fixture, err := relatedfixture.Generate(relatedfixture.CaseMixed, 5000)
		if err != nil {
			b.Fatal(err)
		}
		vaultPath := b.TempDir()
		writeRelatedBenchmarkVault(b, vaultPath, fixture)
		outputPath := filepath.Join(b.TempDir(), "site")
		cfg := testBuildSiteConfig()
		cfg.Related.Enabled = true
		cfg.Related.Count = 5
		if _, err := recommend.Tokenize("分布式数据库一致性协议"); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{force: true, concurrency: 4, diagnosticsWriter: io.Discard})
			if err != nil {
				b.Fatal(err)
			}
			runtime.KeepAlive(result)
		}
	})
}

func TestRSSHelperIsolation(t *testing.T) {
	if specification := os.Getenv(relatedEndToEndRSSHelperEnv); specification != "" {
		kind, countText, ok := strings.Cut(specification, ":")
		if !ok {
			t.Fatalf("invalid end-to-end RSS specification %q", specification)
		}
		count, err := strconv.Atoi(countText)
		if err != nil || count < 0 {
			t.Fatalf("invalid end-to-end RSS count %q", countText)
		}
		fixture, err := relatedfixture.Generate(kind, count)
		if err != nil {
			t.Fatal(err)
		}
		vaultPath := t.TempDir()
		writeRelatedBenchmarkVault(t, vaultPath, fixture)
		cfg := testBuildSiteConfig()
		cfg.Related.Enabled = true
		if _, err := buildWithOptions(cfg, vaultPath, filepath.Join(t.TempDir(), "site"), buildOptions{force: true, concurrency: 4, diagnosticsWriter: io.Discard}); err != nil {
			t.Fatal(err)
		}
		peak, err := readBuildVmHWM()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("RELATED_END_TO_END_RSS %s %d %d\n", kind, count, peak)
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRSSHelperIsolation$", "-test.count=1")
	command.Env = append(os.Environ(), relatedEndToEndRSSHelperEnv+"="+relatedfixture.CaseMixed+":20")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("end-to-end RSS helper error = %v\n%s", err, output)
	}
	if strings.Count(string(output), "RELATED_END_TO_END_RSS ") != 1 {
		t.Fatalf("end-to-end RSS output = %q, want one case", output)
	}
}

func TestMemoryProfileCheckpoints(t *testing.T) {
	profileDir := os.Getenv("OBSITE_RELATED_PROFILE_DIR")
	if profileDir == "" {
		profileDir = t.TempDir()
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	previousProfileRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = previousProfileRate }()

	fixture, err := relatedfixture.Generate(relatedfixture.CaseMixed, 20)
	if err != nil {
		t.Fatal(err)
	}
	vaultPath := t.TempDir()
	writeRelatedBenchmarkVault(t, vaultPath, fixture)
	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	result, err := buildWithOptions(cfg, vaultPath, filepath.Join(t.TempDir(), "site"), buildOptions{force: true, concurrency: 4, diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	file, err := os.Create(filepath.Join(profileDir, "build-page-output.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.WriteHeapProfile(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(result)
}

type relatedBenchmarkTB interface {
	Helper()
	Fatal(args ...any)
}

func writeRelatedBenchmarkVault(tb relatedBenchmarkTB, vaultPath string, fixture relatedfixture.Fixture) {
	tb.Helper()
	for _, document := range fixture.Semantics {
		note := fixture.Index.Notes[document.RelPath]
		var content strings.Builder
		content.WriteString("---\ntitle: ")
		content.WriteString(document.Title)
		content.WriteString("\ndate: 2026-04-06\n")
		if note != nil && len(note.Tags) > 0 {
			content.WriteString("tags:\n")
			for _, tag := range note.Tags {
				content.WriteString("  - ")
				content.WriteString(tag)
				content.WriteByte('\n')
			}
		}
		content.WriteString("---\n# ")
		content.WriteString(document.Title)
		content.WriteString("\n\n")
		content.WriteString(document.Body)
		absolutePath := filepath.Join(vaultPath, filepath.FromSlash(document.RelPath))
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(absolutePath, []byte(content.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
}

func readBuildVmHWM() (int64, error) {
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "VmHWM:" {
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("VmHWM not found")
}
