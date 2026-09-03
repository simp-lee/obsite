package render

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tdewolff/minify/v2"
	mincss "github.com/tdewolff/minify/v2/css"
)

const (
	katexCSSOutputPath  = "assets/obsite-runtime/katex.min.css"
	katexJSOutputPath   = "assets/obsite-runtime/katex.min.js"
	katexAutoOutputPath = "assets/obsite-runtime/auto-render.min.js"
	mermaidJSOutputPath = "assets/obsite-runtime/mermaid.min.js"
)

type embeddedOutputAsset struct {
	name       string
	outputPath string
}

var runtimeTemplateAssets = func() []embeddedOutputAsset {
	assets := []embeddedOutputAsset{
		{name: "vendor/katex/katex.min.css", outputPath: katexCSSOutputPath},
		{name: "vendor/katex/katex.min.js", outputPath: katexJSOutputPath},
		{name: "vendor/katex/contrib/auto-render.min.js", outputPath: katexAutoOutputPath},
		{name: "vendor/mermaid/mermaid.min.js", outputPath: mermaidJSOutputPath},
	}
	fonts, _ := fs.Glob(embeddedSiteFS, "vendor/katex/fonts/*")
	for _, name := range fonts {
		assets = append(assets, embeddedOutputAsset{name: name, outputPath: path.Join("assets/obsite-runtime/fonts", path.Base(name))})
	}
	return assets
}()

type sharedRuntimeFile struct {
	outputPath string
	data       []byte
}

// RuntimeAsset is one fixed offline runtime file ready for owner-registry publication.
type RuntimeAsset struct {
	OutputPath string
	Data       []byte
}

var loadSharedRuntimeFile = sync.OnceValues(func() (sharedRuntimeFile, error) {
	data, err := readEmbeddedAsset("runtime.js")
	if err != nil {
		return sharedRuntimeFile{}, err
	}
	hash := sha256.Sum256(data)
	return sharedRuntimeFile{outputPath: fmt.Sprintf("assets/obsite/runtime.%x.js", hash), data: data}, nil
})

// StyleCSSData returns the fixed built-in stylesheet for owner-registry publication.
func StyleCSSData() ([]byte, error) {
	data, err := readEmbeddedAsset("style.css")
	if err != nil {
		return nil, fmt.Errorf("read style.css: %w", err)
	}
	minifier := minify.New()
	minifier.AddFunc("text/css", mincss.Minify)
	compact, err := minifier.Bytes("text/css", data)
	if err != nil {
		return nil, fmt.Errorf("minify style.css: %w", err)
	}
	return compact, nil
}

// EmitStyleCSS writes the fixed built-in stylesheet into the output root.
func EmitStyleCSS(outputRoot string) (bool, error) {
	if strings.TrimSpace(outputRoot) == "" {
		return false, errors.New("emit style.css: output root is required")
	}
	data, err := StyleCSSData()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return false, fmt.Errorf("emit style.css: mkdir %q: %w", outputRoot, err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "style.css"), data, 0o644); err != nil {
		return false, fmt.Errorf("emit style.css: write style.css: %w", err)
	}
	return true, nil
}

// RuntimeAssetData returns all fixed offline runtime files for owner-registry publication.
func RuntimeAssetData() ([]RuntimeAsset, error) {
	result := make([]RuntimeAsset, 0, len(runtimeTemplateAssets)+1)
	for _, asset := range runtimeTemplateAssets {
		data, err := readEmbeddedAsset(asset.name)
		if err != nil {
			return nil, fmt.Errorf("read runtime asset %s: %w", asset.name, err)
		}
		result = append(result, RuntimeAsset{OutputPath: asset.outputPath, Data: append([]byte(nil), data...)})
	}
	runtimeFile, err := loadSharedRuntimeFile()
	if err != nil {
		return nil, fmt.Errorf("read shared runtime: %w", err)
	}
	result = append(result, RuntimeAsset{OutputPath: runtimeFile.outputPath, Data: append([]byte(nil), runtimeFile.data...)})
	return result, nil
}

// EmitRuntimeAssets writes all fixed offline runtime and vendor assets once.
func EmitRuntimeAssets(outputRoot string) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("emit runtime assets: output root is required")
	}
	assets, err := RuntimeAssetData()
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if err := writeRuntimeAsset(outputRoot, asset.OutputPath, asset.Data); err != nil {
			return err
		}
	}
	return nil
}

func writeRuntimeAsset(outputRoot, outputPath string, data []byte) error {
	assetPath := filepath.Join(outputRoot, filepath.FromSlash(outputPath))
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		return fmt.Errorf("emit runtime assets: mkdir %q: %w", filepath.Dir(assetPath), err)
	}
	if err := os.WriteFile(assetPath, data, 0o644); err != nil {
		return fmt.Errorf("emit runtime assets: write %q: %w", outputPath, err)
	}
	return nil
}

// SharedRuntimeOutputPath returns the content-addressed shared runtime path.
func SharedRuntimeOutputPath() (string, error) {
	runtimeFile, err := loadSharedRuntimeFile()
	if err != nil {
		return "", err
	}
	return runtimeFile.outputPath, nil
}

// RuntimeAssetOutputPaths returns all fixed offline runtime output paths.
func RuntimeAssetOutputPaths() []string {
	paths := make([]string, 0, len(runtimeTemplateAssets)+1)
	for _, asset := range runtimeTemplateAssets {
		paths = append(paths, asset.outputPath)
	}
	if runtimeFile, err := loadSharedRuntimeFile(); err == nil {
		paths = append(paths, runtimeFile.outputPath)
	}
	return paths
}

func readEmbeddedAsset(name string) ([]byte, error) {
	assetPath := name
	if !strings.HasPrefix(assetPath, "vendor/") {
		assetPath = embeddedSiteAssetPath(name)
	}
	data, err := embeddedSiteFS.ReadFile(assetPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded asset %q: %w", name, err)
	}
	return data, nil
}

// EmbeddedRuntimeAssetNames returns the fixed runtime inventory.
func EmbeddedRuntimeAssetNames() []string {
	result := make([]string, 0, len(runtimeTemplateAssets)+2)
	for _, asset := range runtimeTemplateAssets {
		result = append(result, asset.name)
	}
	result = append(result, "runtime.js", "style.css")
	return result
}
