package asset

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// RewriteCSSURLs rewrites URL tokens, imports, and image-set URL strings without
// changing comments, ordinary strings, or URLs the resolver leaves unchanged.
func RewriteCSSURLs(data []byte, resolve func(string) (string, error)) ([]byte, error) {
	input := parse.NewInputBytes(data)
	defer input.Restore()
	lexer := css.NewLexer(input)
	var output bytes.Buffer
	importURL := false
	var imageSet []bool
	for {
		kind, token := lexer.Next()
		start := input.Offset() - len(token)
		if kind == css.ErrorToken {
			if err := lexer.Err(); err != io.EOF {
				return nil, err
			}
			return output.Bytes(), nil
		}
		rawURL := ""
		if candidate, end, ok := quotedURLArgument(input.Bytes()[start:]); ok &&
			(kind == css.BadURLToken || kind == css.URLToken || kind == css.FunctionToken && strings.EqualFold(unescapeCSS(strings.TrimSuffix(string(token), "(")), "url")) {
			input.Move(start + end - input.Offset())
			input.Skip()
			kind = css.URLToken
			token = input.Bytes()[start : start+end]
			rawURL = candidate
		}
		// The lexer exposes hex-escaped spellings such as u\\72l(...) as
		// ordinary functions. Consume their argument as one URL token too.
		if rawURL == "" && kind == css.FunctionToken && strings.EqualFold(unescapeCSS(string(token[:len(token)-1])), "url") {
			// Scan raw URL bytes: unlike general CSS tokens, /* is literal
			// URL content here, not the start of a comment.
			quote := byte(0)
			for input.Err() == nil {
				b := input.Peek(0)
				input.Move(1)
				if b == '\\' && input.Err() == nil {
					input.Move(1)
				} else if quote != 0 {
					if b == quote {
						quote = 0
					}
				} else if b == '\'' || b == '"' {
					quote = b
				} else if b == ')' {
					break
				}
			}
			kind, token = css.URLToken, append(bytes.Clone(token), input.Shift()...)
		}
		isURL := kind == css.URLToken
		isStringURL := kind == css.StringToken && (importURL || len(imageSet) > 0 && imageSet[len(imageSet)-1])
		if isURL || isStringURL {
			raw := rawURL
			if raw == "" {
				raw = string(token)
			}
			if isURL && rawURL == "" {
				raw = strings.TrimSpace(strings.TrimSuffix(raw[strings.IndexByte(raw, '(')+1:], ")"))
			}
			if len(raw) >= 2 && (raw[0] == '\'' || raw[0] == '"') && raw[len(raw)-1] == raw[0] {
				raw = raw[1 : len(raw)-1]
			}
			raw = unescapeCSS(raw)
			replacement, err := resolve(raw)
			if err != nil {
				return nil, err
			}
			if replacement != raw {
				escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\a `, "\r", `\d `, "\f", `\c `).Replace(replacement)
				if isURL {
					fmt.Fprintf(&output, `url("%s")`, escaped)
				} else {
					fmt.Fprintf(&output, `"%s"`, escaped)
				}
			} else {
				output.Write(token)
			}
		} else {
			output.Write(token)
		}
		if kind != css.WhitespaceToken && kind != css.CommentToken {
			importURL = kind == css.AtKeywordToken && strings.EqualFold(unescapeCSS(string(token)), "@import")
		}
		switch kind {
		case css.FunctionToken:
			name := unescapeCSS(string(token[:len(token)-1]))
			imageSet = append(imageSet, strings.EqualFold(name, "image-set") || strings.EqualFold(name, "-webkit-image-set"))
		case css.LeftParenthesisToken:
			imageSet = append(imageSet, false)
		case css.RightParenthesisToken:
			if len(imageSet) > 0 {
				imageSet = imageSet[:len(imageSet)-1]
			}
		}
	}
}

func quotedURLArgument(data []byte) (string, int, bool) {
	i := bytes.IndexByte(data, '(')
	if i < 0 {
		return "", 0, false
	}
	i++
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n' || data[i] == '\f') {
		i++
	}
	if i >= len(data) || data[i] != '\'' && data[i] != '"' {
		return "", 0, false
	}
	quote := data[i]
	start := i + 1
	i++
	for i < len(data) {
		if data[i] == '\\' {
			i += 2
			continue
		}
		if data[i] == quote {
			raw := unescapeCSS(string(data[start:i]))
			i++
			for {
				for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n' || data[i] == '\f') {
					i++
				}
				if i+1 >= len(data) || data[i] != '/' || data[i+1] != '*' {
					break
				}
				end := bytes.Index(data[i+2:], []byte("*/"))
				if end < 0 {
					return "", 0, false
				}
				i += 2 + end + 2
			}
			for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n' || data[i] == '\f') {
				i++
			}
			if i < len(data) && data[i] == ')' {
				return raw, i + 1, true
			}
			return "", 0, false
		}
		i++
	}
	return "", 0, false
}

func unescapeCSS(value string) string {
	var output strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 == len(value) {
			output.WriteByte(value[i])
			continue
		}
		i++
		start := i
		for i < len(value) && i-start < 6 && (value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f' || value[i] >= 'A' && value[i] <= 'F') {
			i++
		}
		if i > start {
			n, _ := strconv.ParseUint(value[start:i], 16, 32)
			r := rune(n)
			if r == 0 || !utf8.ValidRune(r) {
				r = utf8.RuneError
			}
			output.WriteRune(r)
			if i < len(value) && strings.ContainsRune(" \t\r\n\f", rune(value[i])) {
				if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
					i++
				}
			} else {
				i--
			}
		} else if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
			i++
		} else if value[i] != '\r' && value[i] != '\n' && value[i] != '\f' {
			output.WriteByte(value[i])
		}
	}
	return output.String()
}
