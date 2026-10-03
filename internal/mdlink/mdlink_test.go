package mdlink_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/leonhfr/glean/internal/mdlink"
)

func TestLocalCommonMarkLinks(t *testing.T) {
	t.Parallel()
	document := "\xef\xbb\xbf---\r\ndescription: '[metadata](ignored.md)'\r\n---\r\n" +
		"[inline](docs/a%20b.md?q=1#part \"Title\") ![image](img.png)\r\n" +
		"[nested](docs/a(b).md) [escaped](docs/a\\(b\\).md) [entity](a&amp;b.md)\r\n" +
		"[reference][r] [r]\r\n\r\n[r]: <docs/ref file.md> 'title'\r\n" +
		"[multi](\r\n  multiline.md\r\n)\r\n" +
		"[empty]() [bad](bad%ZZ.md)\r\n"
	links := mdlink.Local([]byte(document))
	assert.Equal(t, []mdlink.Link{
		{Line: 4, Raw: "docs/a%20b.md?q=1#part", Path: "docs/a b.md"},
		{Line: 4, Raw: "img.png", Path: "img.png"},
		{Line: 5, Raw: "docs/a(b).md", Path: "docs/a(b).md"},
		{Line: 5, Raw: `docs/a\(b\).md`, Path: "docs/a(b).md"},
		{Line: 5, Raw: "a&amp;b.md", Path: "a&b.md"},
		{Line: 8, Raw: "docs/ref file.md", Path: "docs/ref file.md"},
		{Line: 9, Raw: "multiline.md", Path: "multiline.md"},
		{Line: 12, Raw: "bad%ZZ.md", Invalid: true},
	}, links)
}

func TestLocalIgnoresNonLinksAndExcludedContexts(t *testing.T) {
	t.Parallel()
	document := "`[inline code](ignored.md)`\n\n" +
		"```md\n[fenced](ignored.md)\n```\n\n" +
		"    [indented](ignored.md)\n\n" +
		"~~~\n[tilde](ignored.md)\n~~~\n\n" +
		"<div>\n[html block](ignored.md)\n</div>\n\n" +
		"<a href=\"ignored.md\">html</a>\n" +
		"[external](https://example.test/bad%ZZ) [mailto](mailto:user@example.test)\n" +
		"[absolute](/outside.md) [network](//host/file) [anchor](#part)\n" +
		"[windows](C:/outside.md) [query](?q=1)\n" +
		"prose ignored.md and ](unmatched.md) and [broken](missing\n"
	assert.Empty(t, mdlink.Local([]byte(document)))
}

func TestLocalLeavesInputUnchanged(t *testing.T) {
	t.Parallel()
	source := []byte("---\nname: sample\n---\n[one](a.md)\n")
	before := string(source)
	assert.Len(t, mdlink.Local(source), 1)
	assert.Equal(t, before, string(source))
}
