package asset

import "testing"

func TestValidateLocalSVGAcceptsInternalReferences(t *testing.T) {
	for _, source := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><style>.shape{fill:url(#paint)}</style><path id="paint"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><use href="#shape"/></svg>`,
	} {
		if err := ValidateLocalSVG([]byte(source)); err != nil {
			t.Fatalf("ValidateLocalSVG(%q) error = %v", source, err)
		}
	}
}

func TestValidateLocalSVGRejectsMalformedAndExternalReferences(t *testing.T) {
	for _, source := range []string{
		`<svg><image href="https://example.test/image.png"/></svg>`,
		`<svg><style>@import url("https://example.test/style.css")</style></svg>`,
		`<svg><text>unfinished</svg>`,
		`<?xml-stylesheet href="style.css"?><svg/>`,
	} {
		if err := ValidateLocalSVG([]byte(source)); err == nil {
			t.Fatalf("ValidateLocalSVG(%q) error = nil", source)
		}
	}
}
