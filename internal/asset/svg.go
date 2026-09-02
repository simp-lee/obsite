package asset

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// ValidateLocalSVG requires a well-formed SVG whose image, stylesheet, and
// font references are entirely internal fragment references.
func ValidateLocalSVG(data []byte) error {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	seenRoot := false
	inStyle := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decode SVG XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if !seenRoot {
				if !strings.EqualFold(value.Name.Local, "svg") {
					return fmt.Errorf("root element must be svg")
				}
				seenRoot = true
			}
			inStyle = strings.EqualFold(value.Name.Local, "style")
			for _, attribute := range value.Attr {
				name := strings.ToLower(attribute.Name.Local)
				text := strings.TrimSpace(attribute.Value)
				if (name == "href" || name == "src") && text != "" && !strings.HasPrefix(text, "#") {
					return fmt.Errorf("external SVG reference %q is not allowed", text)
				}
				if name == "style" {
					if err := validateLocalSVGStyle(text); err != nil {
						return err
					}
				}
			}
		case xml.EndElement:
			if strings.EqualFold(value.Name.Local, "style") {
				inStyle = false
			}
		case xml.CharData:
			if inStyle {
				if err := validateLocalSVGStyle(string(value)); err != nil {
					return err
				}
			}
		case xml.ProcInst:
			if strings.EqualFold(value.Target, "xml-stylesheet") {
				return fmt.Errorf("external SVG stylesheet is not allowed")
			}
		}
	}
	if !seenRoot {
		return fmt.Errorf("SVG root element is missing")
	}
	return nil
}

func validateLocalSVGStyle(value string) error {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "@import") {
		return fmt.Errorf("external SVG stylesheet import is not allowed")
	}
	for offset := 0; ; {
		index := strings.Index(lower[offset:], "url(")
		if index < 0 {
			break
		}
		start := offset + index + len("url(")
		end := strings.IndexByte(lower[start:], ')')
		if end < 0 {
			return fmt.Errorf("malformed SVG CSS url() reference")
		}
		reference := strings.Trim(strings.TrimSpace(value[start:start+end]), "'\"")
		if reference != "" && !strings.HasPrefix(reference, "#") {
			return fmt.Errorf("external SVG CSS reference %q is not allowed", reference)
		}
		offset = start + end + 1
	}
	return nil
}
