package comment

import "strings"

// Strip removes Obsidian %% ... %% comments before Markdown parsing.
//
// The implementation keeps a small line-based state machine instead of trying
// to fully parse Markdown. It preserves indented code blocks and fenced code
// blocks, including those nested inside blockquote or list containers.
// Comment contents outside those code block forms are discarded, while
// newlines inside stripped comments are preserved so later parsing keeps
// stable line structure.
func Strip(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}

	dst := make([]byte, 0, len(src))
	inComment := false
	inFence := false
	inIndented := false
	prevBlank := true
	var fenceChar byte
	var fenceLen int
	var activeContainers []container
	var fenceContainers []container
	var indentedContainers []container

	for start := 0; start < len(src); {
		end := start
		for end < len(src) && src[end] != '\n' {
			end++
		}
		if end < len(src) {
			end++
		}

		line := src[start:end]
		lineDstStart := len(dst)

		if inFence {
			contentStart, ok := continueContainers(line, fenceContainers)
			if ok {
				if stripped, nextComment, closes := stripFenceCloserLine(line, contentStart, fenceChar, fenceLen); closes {
					dst = append(dst, stripped...)
					activeContainers = cloneContainers(fenceContainers)
					inFence = false
					fenceChar = 0
					fenceLen = 0
					fenceContainers = nil
					inComment = nextComment
					prevBlank = isBlankContentLine(dst[lineDstStart:], contentStart)
					start = end
					continue
				}

				dst = append(dst, line...)
				activeContainers = cloneContainers(fenceContainers)
				prevBlank = isBlankContentLine(dst[lineDstStart:], contentStart)
				start = end
				continue
			}

			inFence = false
			fenceChar = 0
			fenceLen = 0
			fenceContainers = nil
		}

		if inIndented {
			rawContainers, rawContentStart := parseContainers(line, activeContainers)
			if sameContainers(rawContainers, indentedContainers) && isIndentedCodeContinuationAt(line, rawContentStart) {
				dst = append(dst, line...)
				activeContainers = rawContainers
				prevBlank = isBlankContentLine(dst[lineDstStart:], rawContentStart)
				start = end
				continue
			}

			inIndented = false
			indentedContainers = nil
		}

		visibleLine := stripCommentLine(line, inComment)
		activeContainers, contentStart := parseContainers(visibleLine.text, activeContainers)

		if char, count, ok := fenceOpenerAt(visibleLine.text, contentStart); ok {
			inFence = true
			fenceChar = char
			fenceLen = count
			fenceContainers = cloneContainers(activeContainers)
			dst = append(dst, visibleLine.text...)
			inComment = false
			prevBlank = isBlankContentLine(dst[lineDstStart:], contentStart)
			start = end
			continue
		}

		if prevBlank {
			if codeStart, ok := indentedCodeOpenerAt(visibleLine, line, contentStart); ok {
				inIndented = true
				indentedContainers = cloneContainers(activeContainers)
				indentedLine := buildIndentedCodeLine(visibleLine, line, codeStart)
				dst = append(dst, indentedLine...)
				inComment = false
				prevBlank = isBlankContentLine(dst[lineDstStart:], contentStart)
				start = end
				continue
			}
		}

		dst = append(dst, visibleLine.text...)
		inComment = visibleLine.endsInComment

		prevBlank = isBlankContentLine(dst[lineDstStart:], contentStart)

		start = end
	}

	return dst
}

type containerKind uint8

const (
	containerBlockquote containerKind = iota
	containerList
)

type container struct {
	kind   containerKind
	indent int
}

type strippedLine struct {
	text          []byte
	prefixRawEnds []int
	endsInComment bool
	rawLen        int
}

func fenceOpenerAt(line []byte, start int) (byte, int, bool) {
	index := leadingFenceIndentAt(line, start)
	if index < 0 || index >= len(line) {
		return 0, 0, false
	}

	char := line[index]
	if char != '`' && char != '~' {
		return 0, 0, false
	}

	count := 0
	for index+count < len(line) && line[index+count] == char {
		count++
	}
	if count < 3 {
		return 0, 0, false
	}

	return char, count, true
}

func stripFenceCloserLine(line []byte, start int, char byte, minCount int) ([]byte, bool, bool) {
	index := leadingFenceIndentAt(line, start)
	if index < 0 || index >= len(line) {
		return nil, false, false
	}

	count := 0
	for index+count < len(line) && line[index+count] == char {
		count++
	}
	if count < minCount {
		return nil, false, false
	}

	rest := stripCommentLine(line[index+count:], false)
	if !isBlankLine(rest.text) {
		return nil, false, false
	}

	stripped := make([]byte, 0, index+count+len(rest.text))
	stripped = append(stripped, line[:index+count]...)
	stripped = append(stripped, rest.text...)
	return stripped, rest.endsInComment, true
}

func stripCommentLine(line []byte, inComment bool) strippedLine {
	if len(line) == 0 {
		return strippedLine{
			prefixRawEnds: []int{0},
			endsInComment: inComment,
		}
	}

	stripped := make([]byte, 0, len(line))
	prefixRawEnds := make([]int, 1, len(line)+1)
	for i := 0; i < len(line); {
		if inComment {
			if i+1 < len(line) && line[i] == '%' && line[i+1] == '%' {
				inComment = false
				i += 2
				continue
			}

			if line[i] == '\r' || line[i] == '\n' {
				stripped = append(stripped, line[i])
				prefixRawEnds = append(prefixRawEnds, i+1)
			}

			i++
			continue
		}

		if end, ok := inlineCodeLiteralEnd(line, i); ok {
			stripped, prefixRawEnds = appendRawLiteral(stripped, prefixRawEnds, line[i:end], i)
			i = end
			continue
		}

		if end, ok := inlineHTMLLiteralEnd(line, i); ok {
			stripped, prefixRawEnds = appendRawLiteral(stripped, prefixRawEnds, line[i:end], i)
			i = end
			continue
		}

		if i+1 < len(line) && line[i] == '%' && line[i+1] == '%' {
			inComment = true
			i += 2
			continue
		}

		stripped = append(stripped, line[i])
		prefixRawEnds = append(prefixRawEnds, i+1)
		i++
	}

	return strippedLine{
		text:          stripped,
		prefixRawEnds: prefixRawEnds,
		endsInComment: inComment,
		rawLen:        len(line),
	}
}

func appendRawLiteral(dst []byte, prefixRawEnds []int, raw []byte, rawStart int) ([]byte, []int) {
	for index, ch := range raw {
		dst = append(dst, ch)
		prefixRawEnds = append(prefixRawEnds, rawStart+index+1)
	}
	return dst, prefixRawEnds
}

func inlineCodeLiteralEnd(line []byte, start int) (int, bool) {
	if start >= len(line) || line[start] != '`' {
		return 0, false
	}

	delimLen := 0
	for start+delimLen < len(line) && line[start+delimLen] == '`' {
		delimLen++
	}

	for index := start + delimLen; index < len(line); index++ {
		if line[index] != '`' {
			continue
		}

		count := 0
		for index+count < len(line) && line[index+count] == '`' {
			count++
		}
		if count == delimLen {
			return index + count, true
		}

		index += count - 1
	}

	return 0, false
}

type htmlTagKind uint8

const (
	htmlTagOpen htmlTagKind = iota
	htmlTagClose
	htmlTagOther
)

type htmlTagToken struct {
	name        string
	end         int
	kind        htmlTagKind
	selfClosing bool
}

func inlineHTMLLiteralEnd(line []byte, start int) (int, bool) {
	token, ok := parseHTMLTagToken(line, start)
	if !ok {
		return 0, false
	}
	if token.selfClosing || token.kind != htmlTagOpen {
		return token.end, true
	}

	depth := 1
	for index := token.end; index < len(line); index++ {
		if line[index] != '<' {
			continue
		}

		next, ok := parseHTMLTagToken(line, index)
		if !ok {
			continue
		}
		if next.kind == htmlTagOpen && !next.selfClosing && next.name == token.name {
			depth++
		}
		if next.kind == htmlTagClose && next.name == token.name {
			depth--
			if depth == 0 {
				return next.end, true
			}
		}

		index = next.end - 1
	}

	return 0, false
}

func parseHTMLTagToken(line []byte, start int) (htmlTagToken, bool) {
	if start < 0 || start >= len(line) || line[start] != '<' {
		return htmlTagToken{}, false
	}

	if start+3 < len(line) && line[start+1] == '!' && line[start+2] == '-' && line[start+3] == '-' {
		for index := start + 4; index+2 < len(line); index++ {
			if line[index] == '-' && line[index+1] == '-' && line[index+2] == '>' {
				return htmlTagToken{end: index + 3, kind: htmlTagOther}, true
			}
		}
		return htmlTagToken{}, false
	}

	index := start + 1
	kind := htmlTagOpen
	if index < len(line) && line[index] == '/' {
		kind = htmlTagClose
		index++
	} else if index < len(line) && (line[index] == '!' || line[index] == '?') {
		for ; index < len(line); index++ {
			switch line[index] {
			case '\r', '\n':
				return htmlTagToken{}, false
			case '>':
				return htmlTagToken{end: index + 1, kind: htmlTagOther}, true
			}
		}
		return htmlTagToken{}, false
	}

	if index >= len(line) || !isHTMLTagNameStart(line[index]) {
		return htmlTagToken{}, false
	}

	nameStart := index
	index++
	for index < len(line) && isHTMLTagNameChar(line[index]) {
		index++
	}
	name := strings.ToLower(string(line[nameStart:index]))

	lastNonSpace := byte(0)
	for index < len(line) {
		switch line[index] {
		case '"', '\'':
			quote := line[index]
			index++
			for index < len(line) && line[index] != quote {
				index++
			}
			if index >= len(line) {
				return htmlTagToken{}, false
			}
			index++
		case '\r', '\n':
			return htmlTagToken{}, false
		case '>':
			return htmlTagToken{
				name:        name,
				end:         index + 1,
				kind:        kind,
				selfClosing: kind == htmlTagOpen && (lastNonSpace == '/' || isVoidHTMLTag(name)),
			}, true
		default:
			if line[index] != ' ' && line[index] != '\t' {
				lastNonSpace = line[index]
			}
			index++
		}
	}

	return htmlTagToken{}, false
}

func isHTMLTagNameStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isHTMLTagNameChar(ch byte) bool {
	return isHTMLTagNameStart(ch) || (ch >= '0' && ch <= '9') || ch == '-' || ch == ':' || ch == '_'
}

func isVoidHTMLTag(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func indentedCodeOpenerAt(line strippedLine, raw []byte, start int) (int, bool) {
	codeStart, ok := indentedCodeStartAt(line.text, start)
	if !ok {
		return 0, false
	}

	if hasNonBlankContent(line.text[codeStart:]) {
		return codeStart, true
	}

	rawStart := line.rawOffset(codeStart)
	if rawStart >= len(raw) {
		return 0, false
	}

	if hasNonBlankContent(raw[rawStart:]) {
		return codeStart, true
	}

	return 0, false
}

func buildIndentedCodeLine(line strippedLine, raw []byte, codeStart int) []byte {
	rawStart := line.rawOffset(codeStart)
	if rawStart < 0 {
		rawStart = 0
	}
	if rawStart > len(raw) {
		rawStart = len(raw)
	}

	out := make([]byte, 0, codeStart+len(raw)-rawStart)
	out = append(out, line.text[:codeStart]...)
	out = append(out, raw[rawStart:]...)
	return out
}

func (line strippedLine) rawOffset(pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(line.prefixRawEnds) {
		return line.rawLen
	}
	return line.prefixRawEnds[pos]
}

func indentedCodeStartAt(line []byte, start int) (int, bool) {
	start = clampLineStart(line, start)
	column := 0
	for i := start; i < len(line); i++ {
		switch line[i] {
		case ' ':
			column++
			if column >= 4 {
				return i + 1, true
			}
		case '\t':
			return i + 1, true
		case '\r', '\n':
			return 0, false
		default:
			if column >= 4 {
				return i, true
			}
			return 0, false
		}
	}

	return 0, false
}

func hasNonBlankContent(line []byte) bool {
	for _, ch := range line {
		switch ch {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return true
		}
	}
	return false
}

func leadingFenceIndentAt(line []byte, start int) int {
	if start < 0 || start > len(line) {
		return -1
	}

	index := start
	for index < len(line) && index-start < 3 && line[index] == ' ' {
		index++
	}
	if index < len(line) && line[index] == '\t' {
		return -1
	}
	return index
}

func parseContainers(line []byte, active []container) ([]container, int) {
	containers := make([]container, 0, len(active)+2)
	index := 0

	for _, current := range active {
		next, ok := continueContainer(line, index, current)
		if !ok {
			break
		}
		containers = append(containers, current)
		index = next
	}

	for {
		next, current, ok := consumeContainer(line, index)
		if !ok {
			break
		}
		containers = append(containers, current)
		index = next
	}

	return containers, index
}

func continueContainers(line []byte, active []container) (int, bool) {
	index := 0
	for _, current := range active {
		next, ok := continueContainer(line, index, current)
		if !ok {
			return 0, false
		}
		index = next
	}
	return index, true
}

func continueContainer(line []byte, index int, current container) (int, bool) {
	switch current.kind {
	case containerBlockquote:
		return consumeBlockquoteMarker(line, index)
	case containerList:
		return continueListContainer(line, index, current.indent)
	default:
		return 0, false
	}
}

func consumeContainer(line []byte, index int) (int, container, bool) {
	if next, ok := consumeBlockquoteMarker(line, index); ok {
		return next, container{kind: containerBlockquote}, true
	}

	if next, current, ok := consumeListContainer(line, index); ok {
		return next, current, true
	}

	return 0, container{}, false
}

func consumeBlockquoteMarker(line []byte, index int) (int, bool) {
	i := index
	for i < len(line) && i-index < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != '>' {
		return 0, false
	}
	i++
	if i < len(line) && line[i] == ' ' {
		i++
	}
	return i, true
}

func consumeListContainer(line []byte, index int) (int, container, bool) {
	i := index
	column := 0
	for i < len(line) && column < 3 && line[i] == ' ' {
		i++
		column++
	}

	markerLen := bulletMarkerLength(line, i)
	if markerLen == 0 {
		markerLen = orderedMarkerLength(line, i)
	}
	if markerLen == 0 {
		return 0, container{}, false
	}

	for j := 0; j < markerLen; j++ {
		column++
	}
	i += markerLen

	if i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return 0, container{}, false
	}

	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		column = advanceColumn(column, line[i])
		i++
	}

	return i, container{kind: containerList, indent: column}, true
}

func continueListContainer(line []byte, index int, indent int) (int, bool) {
	if isBlankLine(line[index:]) {
		return len(line), true
	}

	next, ok := consumeIndentColumns(line, index, indent)
	if !ok {
		return 0, false
	}
	return next, true
}

func consumeIndentColumns(line []byte, index int, width int) (int, bool) {
	column := 0
	i := index
	for i < len(line) && column < width {
		switch line[i] {
		case ' ', '\t':
			column = advanceColumn(column, line[i])
			i++
		default:
			return 0, false
		}
	}
	if column < width {
		return 0, false
	}
	return i, true
}

func advanceColumn(column int, ch byte) int {
	if ch == '\t' {
		return column + 4 - (column % 4)
	}
	return column + 1
}

func bulletMarkerLength(line []byte, index int) int {
	if index >= len(line) {
		return 0
	}
	if line[index] == '-' || line[index] == '+' || line[index] == '*' {
		return 1
	}
	return 0
}

func orderedMarkerLength(line []byte, index int) int {
	start := index
	for index < len(line) && index-start < 9 && line[index] >= '0' && line[index] <= '9' {
		index++
	}
	if index == start || index >= len(line) {
		return 0
	}
	if line[index] != '.' && line[index] != ')' {
		return 0
	}
	return index - start + 1
}

func isIndentedCodeLineAt(line []byte, start int) bool {
	if isBlankContentLine(line, start) {
		return false
	}

	column := 0
	for i := clampLineStart(line, start); i < len(line); i++ {
		switch line[i] {
		case ' ':
			column++
		case '\t':
			return true
		case '\r', '\n':
			return false
		default:
			return column >= 4
		}

		if column >= 4 {
			return true
		}
	}

	return false
}

func isIndentedCodeContinuationAt(line []byte, start int) bool {
	return isBlankContentLine(line, start) || isIndentedCodeLineAt(line, start)
}

func isBlankContentLine(line []byte, start int) bool {
	start = clampLineStart(line, start)
	return isBlankLine(line[start:])
}

func clampLineStart(line []byte, start int) int {
	if start < 0 {
		return 0
	}
	if start > len(line) {
		return len(line)
	}
	return start
}

func sameContainers(left, right []container) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

func isBlankLine(line []byte) bool {
	for _, ch := range line {
		switch ch {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return false
		}
	}
	return true
}

func cloneContainers(src []container) []container {
	if len(src) == 0 {
		return nil
	}

	dst := make([]container, len(src))
	copy(dst, src)
	return dst
}
