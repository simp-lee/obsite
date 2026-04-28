package recommend

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

var (
	defaultSegmenterOnce  sync.Once
	defaultSegmenter      gse.Segmenter
	defaultSegmenterErr   error
	loadEmbeddedSegmenter = func(segmenter *gse.Segmenter) error {
		segmenter.SkipLog = true
		return segmenter.LoadDictEmbed()
	}
)

// Tokenize splits mixed-language note content into normalized terms.
// CJK spans are segmented with gse, while non-CJK spans are split only on
// whitespace so engineering terms like node.js or path/to/file stay intact.
func Tokenize(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	tokens := make([]string, 0, 32)
	var latin strings.Builder
	var cjk strings.Builder
	var segmenter *gse.Segmenter

	flushLatin := func() {
		appendNormalizedToken(&tokens, latin.String())
		latin.Reset()
	}
	ensureSegmenter := func() (*gse.Segmenter, error) {
		if segmenter != nil {
			return segmenter, nil
		}

		loaded, err := defaultTokenizerSegmenter()
		if err != nil {
			return nil, err
		}
		segmenter = loaded
		return segmenter, nil
	}
	flushCJK := func() error {
		if cjk.Len() == 0 {
			return nil
		}

		loaded, err := ensureSegmenter()
		if err != nil {
			return err
		}
		for _, token := range loaded.Cut(cjk.String(), true) {
			appendNormalizedToken(&tokens, token)
		}
		cjk.Reset()
		return nil
	}

	for _, r := range text {
		switch {
		case isCJKRune(r):
			flushLatin()
			cjk.WriteRune(r)
		case unicode.IsSpace(r):
			if err := flushCJK(); err != nil {
				return nil, err
			}
			flushLatin()
		default:
			if err := flushCJK(); err != nil {
				return nil, err
			}
			latin.WriteRune(unicode.ToLower(r))
		}
	}

	if err := flushCJK(); err != nil {
		return nil, err
	}
	flushLatin()

	return tokens, nil
}

func defaultTokenizerSegmenter() (*gse.Segmenter, error) {
	defaultSegmenterOnce.Do(func() {
		defaultSegmenterErr = loadEmbeddedSegmenter(&defaultSegmenter)
	})
	if defaultSegmenterErr != nil {
		return nil, fmt.Errorf("load gse dictionary: %w", defaultSegmenterErr)
	}

	return &defaultSegmenter, nil
}

func appendNormalizedToken(dst *[]string, token string) {
	trimmed := strings.TrimSpace(strings.ToLower(token))
	if trimmed == "" {
		return
	}

	for _, r := range trimmed {
		if isWordRune(r) || isCJKRune(r) {
			*dst = append(*dst, trimmed)
			return
		}
	}
}

func isWordRune(r rune) bool {
	if isCJKRune(r) {
		return false
	}

	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isCJKRune(r rune) bool {
	return unicode.In(r,
		unicode.Han,
		unicode.Hiragana,
		unicode.Katakana,
		unicode.Hangul,
	)
}
