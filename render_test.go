package main

import (
	"encoding/json"
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

func TestRender(t *testing.T) {
	source := "# Title\n\n```mermaid\ngraph TD\n  A --> B\n```\n\n```go\nfunc main() {}\n```\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n"
	html := string(render([]byte(source)))
	for _, want := range []string{`<pre class="mermaid">`, "A --&gt; B", `class="chroma"`, "<table>", `type="checkbox"`} {
		if !strings.Contains(html, want) {
			t.Errorf("render() missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, "language-mermaid") {
		t.Errorf("render() left an unconverted mermaid block in\n%s", html)
	}
}
