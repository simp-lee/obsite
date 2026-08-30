package recommend

import (
	"strings"
	"unicode"

	"github.com/simp-lee/obsite/internal/recommend/chinese"
)

// Tokenize splits mixed-language note content into normalized terms.
// Han spans are segmented with the embedded Chinese segmenter, while non-Han
// spans are split only on whitespace so engineering terms stay intact.
func Tokenize(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	tokens := make([]string, 0, 32)
	var latin strings.Builder
	var han strings.Builder
	var segmenter *chinese.Segmenter

	flushLatin := func() {
		appendNormalizedToken(&tokens, latin.String())
		latin.Reset()
	}
	ensureSegmenter := func() (*chinese.Segmenter, error) {
		if segmenter != nil {
			return segmenter, nil
		}

		loaded, err := chinese.Default()
		if err != nil {
			return nil, err
		}
		segmenter = loaded
		return segmenter, nil
	}
	flushHan := func() error {
		if han.Len() == 0 {
			return nil
		}

		loaded, err := ensureSegmenter()
		if err != nil {
			return err
		}
		for _, token := range loaded.Cut(han.String(), true) {
			appendNormalizedToken(&tokens, token)
		}
		han.Reset()
		return nil
	}

	for _, r := range text {
		switch {
		case isHanRune(r):
			flushLatin()
			han.WriteRune(r)
		case unicode.IsSpace(r):
			if err := flushHan(); err != nil {
				return nil, err
			}
			flushLatin()
		default:
			if err := flushHan(); err != nil {
				return nil, err
			}
			latin.WriteRune(unicode.ToLower(r))
		}
	}

	if err := flushHan(); err != nil {
		return nil, err
	}
	flushLatin()

	return tokens, nil
}

func appendNormalizedToken(dst *[]string, token string) {
	trimmed := strings.TrimSpace(strings.ToLower(token))
	if trimmed == "" {
		return
	}

	for _, r := range trimmed {
		if isWordRune(r) || isHanRune(r) {
			*dst = append(*dst, trimmed)
			return
		}
	}
}

func isWordRune(r rune) bool {
	if isHanRune(r) {
		return false
	}

	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isHanRune(r rune) bool {
	return unicode.Is(unicode.Han, r)
}
