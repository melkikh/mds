package main

import (
	"bytes"
	"cmp"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

type flavor string

const (
	markdownFlavor flavor = "md"
	yfmFlavor      flavor = "yfm"
)

func newMarkdown(yfm bool) goldmark.Markdown {
	extensions := []goldmark.Extender{
		extension.GFM,
		extension.Typographer,
		highlighting.NewHighlighting(highlighting.WithFormatOptions(chromahtml.WithClasses(true))),
	}
	parserOptions := []parser.Option{parser.WithAutoHeadingID()}
	if yfm {
		extensions = append(extensions, yfmExtension{})
		parserOptions = append(parserOptions, parser.WithAttribute())
	}
	return goldmark.New(
		goldmark.WithExtensions(extensions...),
		goldmark.WithParserOptions(parserOptions...),
		goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe()),
	)
}

var markdown = newMarkdown(false)

var yfmMarkdown = newMarkdown(true)

var mermaidBlock = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

var frontmatter = regexp.MustCompile(`(?s)\A---[ \t]*\r?\n(.*?)\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|\z)`)

type node struct {
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Children []*node `json:"children,omitempty"`
}

func render(source []byte) (template.HTML, bool) {
	return renderFlavor(source, markdownFlavor)
}

func renderFlavor(source []byte, flavor flavor) (template.HTML, bool) {
	head := ""
	if match := frontmatter.FindSubmatchIndex(source); match != nil {
		head = renderFrontmatter(string(source[match[2]:match[3]]))
		source = source[match[1]:]
	}
	var buf bytes.Buffer
	ids := parser.WithIDs(&anchors{used: map[string]bool{}})
	renderer := markdown
	if flavor == yfmFlavor {
		renderer = yfmMarkdown
	}
	if err := renderer.Convert(source, &buf, parser.WithContext(parser.NewContext(ids))); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(err.Error()) + "</pre>"), false
	}
	body := buf.String()
	diagrams := mermaidBlock.MatchString(body)
	return template.HTML(head + mermaidBlock.ReplaceAllString(body, `<pre class="mermaid">$1</pre>`)), diagrams
}

// anchors names headings the way github does, because that is what the link in a table of
// contents was written against. Goldmark's own generator walks the title a byte at a time
// and throws away everything wider than ascii, so every russian heading in a document ends
// up as id="-" and nothing in it can be jumped to.
type anchors struct{ used map[string]bool }

func (a *anchors) Generate(title []byte, kind ast.NodeKind) []byte {
	slug := slugify(string(title))
	if slug == "" {
		slug = "heading"
		if kind != ast.KindHeading {
			slug = "id"
		}
	}
	taken := slug
	for n := 1; a.used[taken]; n++ {
		taken = fmt.Sprintf("%s-%d", slug, n)
	}
	a.used[taken] = true
	return []byte(taken)
}

// Put records an id the document wrote out itself, so a later heading cannot take it.
func (a *anchors) Put(id []byte) { a.used[string(id)] = true }

func slugify(title string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			slug.WriteRune(r)
		case unicode.IsSpace(r):
			slug.WriteByte('-')
		}
	}
	return slug.String()
}

// renderFrontmatter folds the block behind a fixed { }, never behind a line of the yaml
// itself: what stands there would otherwise be whatever the file happens to start with.
func renderFrontmatter(block string) string {
	if strings.TrimSpace(block) == "" {
		return ""
	}
	return `<details class="frontmatter"><summary title="frontmatter">{ }</summary><pre>` +
		template.HTMLEscapeString(block) + "</pre></details>"
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
