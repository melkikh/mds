package main

import (
	"strings"
	"testing"
)

func TestDetectYFM(t *testing.T) {
	for _, source := range []string{
		"{% cut \"details\" %}\n",
		"  {% note warning %}\n",
		"{% list tabs group=os %}\n",
		"#|\n|| a | b ||\n|#\n",
		"{% include [shared](../_includes/shared.md) %}\n",
	} {
		if !detectYFM([]byte(source)) {
			t.Errorf("detectYFM(%q) = false, so a YFM page would show its control syntax", source)
		}
	}
	for _, source := range []string{
		"ordinary **markdown**\n",
		"```md\n{% cut \"documented syntax\" %}\n```\n",
		"~~~yfm\n#|\n|| documented | table ||\n|#\n~~~\n",
		"    {% note info %}\n\n    documented syntax\n",
		"---\nexample: '{% cut documented %}'\n---\n\nordinary text\n",
		"a heading {#an-anchor}\n",
		"Super^script^ and ==marked== text\n",
	} {
		if detectYFM([]byte(source)) {
			t.Errorf("detectYFM(%q) = true, so ordinary Markdown would unexpectedly change dialect", source)
		}
	}
}

func TestRenderYFMBlocks(t *testing.T) {
	source := `# Page {#own-anchor}

Text with Super^script^, ##monospace##, ==marked== and ++inserted++.

{% note warning "Careful" %}

This is **important**.

{% endnote %}

{% cut "More" %}{#more name=one}

Hidden text.

{% endcut %}

{% list tabs group=os %}

- Linux

  Use Linux.

- macOS {selected}

  Use macOS.

{% endlist %}
`
	html, _ := renderFlavor([]byte(source), yfmFlavor)
	for _, want := range []string{
		`<h1 id="own-anchor">Page</h1>`,
		`Super<sup>script</sup>`,
		`<code>monospace</code>`,
		`<mark>marked</mark>`,
		`<ins>inserted</ins>`,
		`<aside class="yfm-note yfm-note-warning">`,
		`<div class="yfm-note-title">Careful</div>`,
		`<strong>important</strong>`,
		`<details class="yfm-cut" id="more" data-group="one">`,
		`<div class="yfm-tabs" data-group="os">`,
		`<section class="yfm-tab" data-title="Linux">`,
		`<section class="yfm-tab" data-title="macOS" data-selected>`,
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("YFM output has no %q, so the construct would remain unreadable:\n%s", want, html)
		}
	}
	if strings.Contains(string(html), "{%") {
		t.Errorf("YFM control syntax leaked into the rendered page:\n%s", html)
	}
}

func TestRenderYFMMultilineTable(t *testing.T) {
	source := `#|
|:{header-rows="1"}
|| Name | Details ||
|| first
line
|
- one
- two ||
|| spanning | > ||
|| ^ | last ||
|#
`
	html, _ := renderFlavor([]byte(source), yfmFlavor)
	for _, want := range []string{
		`<table class="yfm-table">`,
		`<th scope="col">`,
		"first\nline",
		"<ul>",
		`rowspan="2"`,
		`colspan="2"`,
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("multiline table output has no %q, so structured cells would be lost:\n%s", want, html)
		}
	}
}

func TestRenderYFMTableKeepsNestedMarkupTogether(t *testing.T) {
	source := `#|
|| outer
|
#|
|| nested | table ||
|#
after ||
|| escaped \| pipe | second ||
|#
`
	html, _ := renderFlavor([]byte(source), yfmFlavor)
	if strings.Count(string(html), `<table class="yfm-table">`) != 2 {
		t.Errorf("a nested multiline table was split into outer cells:\n%s", html)
	}
	if !strings.Contains(string(html), "escaped | pipe") {
		t.Errorf("an escaped cell separator changed the table shape:\n%s", html)
	}
}

func TestMarkdownDoesNotInterpretYFM(t *testing.T) {
	html, _ := renderFlavor([]byte("{% note info %}\n\nbody\n\n{% endnote %}\n"), markdownFlavor)
	if strings.Contains(string(html), "yfm-note") || !strings.Contains(string(html), "{% note info %}") {
		t.Errorf("the Markdown renderer changed YFM syntax, so the manual override would not restore the source view:\n%s", html)
	}
}

func TestUnclosedYFMBlockStaysVisible(t *testing.T) {
	html, _ := renderFlavor([]byte("{% cut \"unfinished\" %}\n\ntext being edited\n"), yfmFlavor)
	if strings.Contains(string(html), `class="yfm-cut"`) || !strings.Contains(string(html), "{% cut") {
		t.Errorf("an unfinished YFM block swallowed the rest of the page instead of leaving an editable marker:\n%s", html)
	}
}

func TestYFMNoteCanOmitItsTitle(t *testing.T) {
	html, _ := renderFlavor([]byte("{% note info \"\" %}\n\nquiet\n\n{% endnote %}\n"), yfmFlavor)
	if strings.Contains(string(html), "yfm-note-title") || !strings.Contains(string(html), "quiet") {
		t.Errorf("an explicitly empty YFM note title left an empty heading in the page:\n%s", html)
	}
}
