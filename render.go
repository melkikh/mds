package main

import (
	"bytes"
	"cmp"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Typographer,
		highlighting.NewHighlighting(highlighting.WithFormatOptions(chromahtml.WithClasses(true))),
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe()),
)

var mermaidBlock = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

var frontmatter = regexp.MustCompile(`(?s)\A---[ \t]*\r?\n(.*?)\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|\z)`)

type node struct {
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Children []*node `json:"children,omitempty"`
}

func render(source []byte) (template.HTML, bool) {
	head := ""
	if match := frontmatter.FindSubmatchIndex(source); match != nil {
		head = renderFrontmatter(string(source[match[2]:match[3]]))
		source = source[match[1]:]
	}
	var buf bytes.Buffer
	if err := markdown.Convert(source, &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(err.Error()) + "</pre>"), false
	}
	body := buf.String()
	diagrams := mermaidBlock.MatchString(body)
	return template.HTML(head + mermaidBlock.ReplaceAllString(body, `<pre class="mermaid">$1</pre>`)), diagrams
}

func renderFrontmatter(block string) string {
	escape, rows := template.HTMLEscapeString, []string{}
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		key, value, pair := strings.Cut(line, ":")
		if pair && trimmed != "" && !strings.HasPrefix(trimmed, "-") {
			rows = append(rows, "<dt>"+escape(strings.TrimSpace(key))+"</dt><dd>"+escape(strings.TrimSpace(value)))
			continue
		}
		if trimmed == "" || len(rows) == 0 {
			continue
		}
		last, separator := rows[len(rows)-1], " "
		if strings.HasSuffix(last, "<dd>") {
			separator = ""
		} else if strings.HasPrefix(trimmed, "-") {
			separator = ", "
		}
		rows[len(rows)-1] = last + separator + escape(strings.TrimPrefix(trimmed, "- "))
	}
	if len(rows) == 0 {
		return ""
	}
	return `<dl class="frontmatter">` + strings.Join(rows, "</dd>") + "</dd></dl>"
}

func scan(root string, depth int, skip []string) (dirs, files []string) {
	_ = filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		rel, err := filepath.Rel(root, p)
		if walkErr != nil || err != nil {
			return nil
		}
		switch {
		case !entry.IsDir():
			if isMarkdown(p) {
				files = append(files, filepath.ToSlash(rel))
			}
		case rel != "." && (slices.Contains(skip, entry.Name()) || strings.HasPrefix(entry.Name(), ".") ||
			depth >= 0 && strings.Count(rel, string(os.PathSeparator)) >= depth):
			return fs.SkipDir
		default:
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs, files
}

func buildTree(files []string) []*node {
	root := &node{}
	dirs := map[string]*node{}
	for _, file := range files {
		parts := strings.Split(file, "/")
		parent := root
		for i := 0; i < len(parts)-1; i++ {
			key := strings.Join(parts[:i+1], "/")
			dir, ok := dirs[key]
			if !ok {
				dir = &node{Type: "dir", Name: parts[i], Path: key}
				dirs[key] = dir
				parent.Children = append(parent.Children, dir)
			}
			parent = dir
		}
		parent.Children = append(parent.Children, &node{Type: "file", Name: parts[len(parts)-1], Path: file})
	}
	sortNodes(root.Children)
	return root.Children
}

func prefixPaths(nodes []*node, prefix string) {
	if prefix == "" {
		return
	}
	for _, n := range nodes {
		n.Path = prefix + "/" + n.Path
		prefixPaths(n.Children, prefix)
	}
}

func sortNodes(nodes []*node) {
	slices.SortFunc(nodes, func(a, b *node) int {
		return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Name, b.Name))
	})
	for _, n := range nodes {
		sortNodes(n.Children)
	}
}

func isMarkdown(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".md" || ext == ".markdown"
}
