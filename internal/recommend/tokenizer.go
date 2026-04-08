package recommend

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

var (
	defaultSegmenterOnce sync.Once
	defaultSegmenter     gse.Segmenter
	defaultSegmenterErr  error
)

// Tokenize splits mixed-language note content into normalized terms.
// CJK spans are segmented with gse, while non-CJK letters and numbers use a
// whitespace-style tokenizer.
func Tokenize(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	segmenter, err := defaultTokenizerSegmenter()
	if err != nil {
		return nil, err
	}

	tokens := make([]string, 0, 32)
	var latin strings.Builder
	var cjk strings.Builder

	flushLatin := func() {
		appendNormalizedToken(&tokens, latin.String())
		latin.Reset()
	}
	flushCJK := func() {
		if cjk.Len() == 0 {
			return
		}
		for _, token := range segmenter.Cut(cjk.String(), true) {
			appendNormalizedToken(&tokens, token)
		}
		cjk.Reset()
	}

	for _, r := range text {
		switch {
		case isCJKRune(r):
			flushLatin()
			cjk.WriteRune(r)
		case isWordRune(r):
			flushCJK()
			latin.WriteRune(unicode.ToLower(r))
		default:
			flushCJK()
			flushLatin()
		}
	}

	flushCJK()
	flushLatin()

	return tokens, nil
}

func defaultTokenizerSegmenter() (*gse.Segmenter, error) {
	defaultSegmenterOnce.Do(func() {
		defaultSegmenter.SkipLog = true
		defaultSegmenterErr = defaultSegmenter.LoadDictEmbed()
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
