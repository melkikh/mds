package main

import (
	"encoding/json"
	"html/template"
	"slices"
	"strings"
	"testing"
)

func TestIsMarkdown(t *testing.T) {
	for name, want := range map[string]bool{
		"a.md": true, "a.MD": true, "a.markdown": true,
		"dir/b.md": true, "a.txt": false, "a": false, "md": false,
	} {
		if got := isMarkdown(name); got != want {
			t.Errorf("isMarkdown(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScan(t *testing.T) {
	root := fixture(t)
	cases := []struct {
		depth int
		skip  []string
		files []string
	}{
		{5, defaultSkip, []string{"README.md", "docs/api/deep/deep.md", "docs/api/spec.markdown", "docs/intro.md"}},
		{0, defaultSkip, []string{"README.md"}},
		{1, defaultSkip, []string{"README.md", "docs/intro.md"}},
		{-1, defaultSkip, []string{"README.md", "docs/api/deep/deep.md", "docs/api/spec.markdown", "docs/intro.md"}},
		{5, append(slices.Clone(defaultSkip), "docs"), []string{"README.md"}},
	}
	for _, c := range cases {
		_, files := scan(root, c.depth, c.skip)
		if !slices.Equal(files, c.files) {
			t.Errorf("scan(depth=%d, skip=%q) = %q, want %q", c.depth, c.skip, files, c.files)
		}
	}
	dirs, _ := scan(root, 5, defaultSkip)
	if len(dirs) != 4 {
		t.Errorf("scan dirs = %q, want root, docs, docs/api, docs/api/deep", dirs)
	}
}

func TestBuildTree(t *testing.T) {
	tree := buildTree([]string{"z.md", "docs/b.md", "docs/a/x.md", "a.md"})
	encoded, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"dir","name":"docs","path":"docs","children":[` +
		`{"type":"dir","name":"a","path":"docs/a","children":[{"type":"file","name":"x.md","path":"docs/a/x.md"}]},` +
		`{"type":"file","name":"b.md","path":"docs/b.md"}]},` +
		`{"type":"file","name":"a.md","path":"a.md"},` +
		`{"type":"file","name":"z.md","path":"z.md"}]`
	if string(encoded) != want {
		t.Errorf("buildTree =\n%s\nwant\n%s", encoded, want)
	}
}

func renderHTML(source string) string {
	html, _ := render([]byte(source))
	return string(html)
}

func TestRenderFrontmatter(t *testing.T) {
	html := renderHTML("---\nname: mds\ndescription: |\n  first line\n\n  after a blank line\ntools:\n  - Bash\n  - Sed\n---\n\n# Title\n")
	for _, want := range []string{
		`<details class="frontmatter"><summary title="frontmatter">{ }</summary><pre>`,
		"name: mds\ndescription: |\n  first line\n\n  after a blank line\ntools:\n  - Bash\n  - Sed",
		`<h1 id="title">Title</h1>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("render() missing %q in\n%s", want, html)
		}
	}
	for _, glued := range []string{"first line after", "Bash, Sed", "| first"} {
		if strings.Contains(html, glued) {
			t.Errorf("frontmatter lost its line breaks, %q showed up in\n%s", glued, html)
		}
	}
	if strings.Contains(html, "<dl") || strings.Contains(html, "<dt>") {
		t.Errorf("frontmatter is served verbatim now, not parsed into a table:\n%s", html)
	}
	if strings.Contains(html, "<hr") || strings.Contains(html, "<h2") {
		t.Errorf("frontmatter leaked into the body:\n%s", html)
	}
	body := renderHTML("# Title\n\n---\n\ntail\n")
	if !strings.Contains(body, "<hr") || strings.Contains(body, "frontmatter") {
		t.Errorf("a thematic break mid-document must stay a rule:\n%s", body)
	}
	if got := renderHTML("---\n---\n\n# Title\n"); strings.Contains(got, "frontmatter") {
		t.Errorf("empty frontmatter should render nothing:\n%s", got)
	}
}

func TestFrontmatterSummary(t *testing.T) {
	for _, block := range []string{
		"name: mds\n",
		"title: whatever the file starts with\n",
		"- one\n- two\n",
		"name: <script>\n",
	} {
		html := renderHTML("---\n" + block + "---\n\n# Title\n")
		summary, _, _ := strings.Cut(html, "</summary>")
		if _, label, _ := strings.Cut(summary, "<summary"); label != ` title="frontmatter">{ }` {
			t.Errorf("render(%q) folds to %q, want a fixed { } and none of the yaml", block, label)
		}
		if !strings.Contains(html, "<pre>"+template.HTMLEscapeString(block[:len(block)-1])+"</pre>") {
			t.Errorf("render(%q) lost the block itself:\n%s", block, html)
		}
	}
}

func TestRender(t *testing.T) {
	source := "# Title\n\n```mermaid\ngraph TD\n  A --> B\n```\n\n```go\nfunc main() {}\n```\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n"
	html, diagrams := render([]byte(source))
	for _, want := range []string{`<pre class="mermaid">`, "A --&gt; B", `class="chroma"`, "<table>", `type="checkbox"`} {
		if !strings.Contains(string(html), want) {
			t.Errorf("render() missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(string(html), "language-mermaid") {
		t.Errorf("render() left an unconverted mermaid block in\n%s", html)
	}
	if !diagrams {
		t.Error("render() did not report a mermaid block, so the page would not load mermaid")
	}
	if _, diagrams := render([]byte("# Title\n\n```go\nfunc main() {}\n```\n")); diagrams {
		t.Error("render() reported a mermaid block in a document that has none")
	}
}
