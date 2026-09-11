package main

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var yfmMarker = regexp.MustCompile(`^\s*{%\s*(?:cut|endcut|note|endnote|list\s+tabs|endlist|include|if|elsif|else|endif|for|endfor)\b`)

// detectYFM only trusts syntax that ordinary prose is very unlikely to contain. Fenced
// examples do not count: a page teaching YFM should not turn itself into one.
func detectYFM(source []byte) bool {
	if match := frontmatter.FindSubmatchIndex(source); match != nil {
		source = source[match[1]:]
	}
	fence := byte(0)
	fenceLen := 0
	for _, raw := range bytes.Split(source, []byte("\n")) {
		line := bytes.TrimLeft(raw, " \t")
		indent := len(raw) - len(line)
		if indent <= 3 && len(line) >= 3 && (line[0] == '`' || line[0] == '~') {
			n := 0
			for n < len(line) && line[n] == line[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence, fenceLen = line[0], n
					continue
				}
				if line[0] == fence && n >= fenceLen && len(bytes.TrimSpace(line[n:])) == 0 {
					fence, fenceLen = 0, 0
				}
				continue
			}
		}
		if fence != 0 {
			continue
		}
		if indent >= 4 || len(raw) > 0 && raw[0] == '\t' {
			continue
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "#|" || yfmMarker.MatchString(trimmed) {
			return true
		}
	}
	return false
}

type yfmExtension struct{}

func (yfmExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(
			util.Prioritized(&yfmDirectiveParser{}, 150),
			util.Prioritized(&yfmTableParser{}, 160),
			util.Prioritized(&yfmTabParser{}, 250),
		),
		parser.WithInlineParsers(
			util.Prioritized(&yfmMonospaceParser{}, 90),
			util.Prioritized(newYFMSpanParser('^', 1, "sup"), 500),
			util.Prioritized(newYFMSpanParser('+', 2, "ins"), 500),
			util.Prioritized(newYFMSpanParser('=', 2, "mark"), 500),
		),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&yfmHTMLRenderer{}, 500),
	))
}

const (
	yfmNote = "note"
	yfmCut  = "cut"
	yfmTabs = "tabs"
	yfmTab  = "tab"
)

var kindYFMBlock = ast.NewNodeKind("YFMBlock")

type yfmBlock struct {
	ast.BaseBlock
	typ      string
	variant  string
	title    string
	group    string
	id       string
	selected bool
}

func (n *yfmBlock) Kind() ast.NodeKind { return kindYFMBlock }

func (n *yfmBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"type": n.typ, "title": n.title}, nil)
}

var (
	noteStart = regexp.MustCompile(`^\s*{%\s*note\s+(info|tip|warning|alert)(?:\s+"([^"]*)")?\s*%}\s*$`)
	cutStart  = regexp.MustCompile(`^\s*{%\s*cut\s+"([^"]*)"\s*%}\s*(?:\{([^}]*)})?\s*$`)
	tabsStart = regexp.MustCompile(`^\s*{%\s*list\s+tabs(?:\s+([^%]*?))?\s*%}\s*$`)
	idAttr    = regexp.MustCompile(`(?:^|\s)#([\w.:-]+)(?:\s|$)`)
	groupAttr = regexp.MustCompile(`(?:^|\s)(?:group|name)=([^\s}]+)`)
)

type yfmDirectiveParser struct{}

func (*yfmDirectiveParser) Trigger() []byte { return []byte{'{'} }

func (*yfmDirectiveParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	value := string(line)
	var node *yfmBlock
	if match := noteStart.FindStringSubmatch(value); match != nil {
		title := map[string]string{"info": "note", "tip": "tip", "warning": "important", "alert": "warning"}[match[1]]
		positions := noteStart.FindStringSubmatchIndex(value)
		if positions[4] >= 0 {
			title = match[2]
		}
		node = &yfmBlock{typ: yfmNote, variant: match[1], title: title}
	} else if match := cutStart.FindStringSubmatch(value); match != nil {
		node = &yfmBlock{typ: yfmCut, title: match[1]}
		if id := idAttr.FindStringSubmatch(match[2]); id != nil {
			node.id = id[1]
		}
		if group := groupAttr.FindStringSubmatch(match[2]); group != nil {
			node.group = group[1]
		}
	} else if match := tabsStart.FindStringSubmatch(value); match != nil {
		node = &yfmBlock{typ: yfmTabs}
		if group := groupAttr.FindStringSubmatch(match[1]); group != nil {
			node.group = group[1]
		}
	} else {
		return nil, parser.NoChildren
	}
	if !hasYFMEnd(reader, node.typ) {
		return nil, parser.NoChildren
	}
	reader.AdvanceToEOL()
	return node, parser.HasChildren
}

func hasYFMEnd(reader text.Reader, typ string) bool {
	lineNum, pos := reader.Position()
	defer reader.SetPosition(lineNum, pos)
	reader.AdvanceLine()
	end := map[string]string{yfmNote: "{% endnote %}", yfmCut: "{% endcut %}", yfmTabs: "{% endlist %}"}[typ]
	depth := 0
	fence := byte(0)
	for {
		line, _ := reader.PeekLine()
		if line == nil {
			return false
		}
		trimmed := strings.TrimSpace(string(line))
		plain := strings.TrimLeft(string(line), " \t")
		if len(plain) >= 3 && (strings.HasPrefix(plain, "```") || strings.HasPrefix(plain, "~~~")) {
			if fence == 0 {
				fence = plain[0]
			} else if fence == plain[0] {
				fence = 0
			}
			reader.AdvanceLine()
			continue
		}
		if fence == 0 {
			startsSame := typ == yfmNote && noteStart.Match(line) ||
				typ == yfmCut && cutStart.Match(line) ||
				typ == yfmTabs && tabsStart.Match(line)
			if startsSame {
				depth++
			} else if trimmed == end {
				if depth == 0 {
					return true
				}
				depth--
			}
		}
		reader.AdvanceLine()
	}
}

func (*yfmDirectiveParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	end := map[string]string{yfmNote: "{% endnote %}", yfmCut: "{% endcut %}", yfmTabs: "{% endlist %}"}[node.(*yfmBlock).typ]
	if strings.TrimSpace(string(line)) == end {
		reader.AdvanceToEOL()
		return parser.Close
	}
	return parser.Continue | parser.HasChildren
}

func (*yfmDirectiveParser) Close(ast.Node, text.Reader, parser.Context) {}
func (*yfmDirectiveParser) CanInterruptParagraph() bool                 { return true }
func (*yfmDirectiveParser) CanAcceptIndentedLine() bool                 { return false }

type yfmTabParser struct{}

func (*yfmTabParser) Trigger() []byte { return []byte{'-'} }

func (*yfmTabParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	tabs, ok := parent.(*yfmBlock)
	if !ok || tabs.typ != yfmTabs || pc.BlockIndent() > 3 {
		return nil, parser.NoChildren
	}
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos+1 >= len(line) || line[pos] != '-' || line[pos+1] != ' ' {
		return nil, parser.NoChildren
	}
	title := strings.TrimSpace(string(line[pos+2:]))
	selected := false
	if strings.HasSuffix(title, "{selected}") {
		title = strings.TrimSpace(strings.TrimSuffix(title, "{selected}"))
		selected = true
	}
	reader.AdvanceToEOL()
	return &yfmBlock{typ: yfmTab, title: title, selected: selected}, parser.HasChildren
}

func (*yfmTabParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, segment := reader.PeekLine()
	if util.IsBlank(line) {
		reader.AdvanceToEOL()
		return parser.Continue | parser.HasChildren
	}
	indent, pos := util.IndentWidth(line, reader.LineOffset())
	trimmed := strings.TrimSpace(string(line))
	if indent < 2 && (trimmed == "{% endlist %}" || pos+1 < len(line) && line[pos] == '-' && line[pos+1] == ' ') {
		return parser.Close
	}
	if indent < 2 {
		return parser.Close
	}
	pos, padding := util.IndentPositionPadding(line, reader.LineOffset(), segment.Padding, 2)
	reader.AdvanceAndSetPadding(pos, padding)
	return parser.Continue | parser.HasChildren
}

func (*yfmTabParser) Close(ast.Node, text.Reader, parser.Context) {}
func (*yfmTabParser) CanInterruptParagraph() bool                 { return true }
func (*yfmTabParser) CanAcceptIndentedLine() bool                 { return false }

var kindYFMTable = ast.NewNodeKind("YFMTable")

type yfmTable struct {
	ast.BaseBlock
	body string
}

func (*yfmTable) Kind() ast.NodeKind { return kindYFMTable }

func (n *yfmTable) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type yfmTableParser struct{}

func (*yfmTableParser) Trigger() []byte { return []byte{'#'} }

func (*yfmTableParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	if strings.TrimSpace(string(line)) != "#|" {
		return nil, parser.NoChildren
	}
	lineNum, pos := reader.Position()
	reader.AdvanceLine()
	var body strings.Builder
	depth := 0
	for {
		line, _ = reader.PeekLine()
		if line == nil {
			reader.SetPosition(lineNum, pos)
			return nil, parser.NoChildren
		}
		trimmed := strings.TrimSpace(string(line))
		switch trimmed {
		case "#|":
			depth++
		case "|#":
			if depth == 0 {
				reader.AdvanceLine()
				return &yfmTable{body: body.String()}, parser.NoChildren
			}
			depth--
		}
		body.Write(line)
		reader.AdvanceLine()
	}
}

func (*yfmTableParser) Continue(ast.Node, text.Reader, parser.Context) parser.State {
	return parser.Close
}
func (*yfmTableParser) Close(ast.Node, text.Reader, parser.Context) {}
func (*yfmTableParser) CanInterruptParagraph() bool                 { return true }
func (*yfmTableParser) CanAcceptIndentedLine() bool                 { return false }

var kindYFMSpan = ast.NewNodeKind("YFMSpan")

type yfmSpan struct {
	ast.BaseInline
	tag string
}

func (n *yfmSpan) Kind() ast.NodeKind { return kindYFMSpan }

func (n *yfmSpan) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"tag": n.tag}, nil)
}

type yfmSpanProcessor struct {
	char  byte
	width int
	tag   string
}

func (p *yfmSpanProcessor) IsDelimiter(b byte) bool { return b == p.char }

func (p *yfmSpanProcessor) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == p.char && closer.Char == p.char &&
		opener.OriginalLength == p.width && closer.OriginalLength == p.width
}

func (p *yfmSpanProcessor) OnMatch(int) ast.Node { return &yfmSpan{tag: p.tag} }

type yfmSpanParser struct{ processor *yfmSpanProcessor }

func newYFMSpanParser(char byte, width int, tag string) parser.InlineParser {
	return &yfmSpanParser{processor: &yfmSpanProcessor{char: char, width: width, tag: tag}}
}

func (p *yfmSpanParser) Trigger() []byte { return []byte{p.processor.char} }

func (p *yfmSpanParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	before := reader.PrecendingCharacter()
	line, segment := reader.PeekLine()
	node := parser.ScanDelimiter(line, before, p.processor.width, p.processor)
	if node == nil || node.OriginalLength != p.processor.width || before == rune(p.processor.char) {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	reader.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

func (*yfmSpanParser) CloseBlock(ast.Node, parser.Context) {}

type yfmMonospaceParser struct{}

func (*yfmMonospaceParser) Trigger() []byte { return []byte{'#'} }

func (*yfmMonospaceParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, segment := reader.PeekLine()
	if len(line) < 4 || line[0] != '#' || line[1] != '#' || len(line) > 2 && line[2] == '#' {
		return nil
	}
	end := -1
	for i := 2; i+1 < len(line); i++ {
		if line[i] == '#' && line[i+1] == '#' && (i+2 >= len(line) || line[i+2] != '#') {
			end = i
			break
		}
	}
	if end < 2 {
		return nil
	}
	node := ast.NewCodeSpan()
	if end > 2 {
		node.AppendChild(node, ast.NewRawTextSegment(text.NewSegment(segment.Start+2, segment.Start+end)))
	}
	reader.Advance(end + 2)
	return node
}

func (*yfmMonospaceParser) CloseBlock(ast.Node, parser.Context) {}

type yfmHTMLRenderer struct{}

func (*yfmHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindYFMBlock, renderYFMBlock)
	reg.Register(kindYFMTable, renderYFMTable)
	reg.Register(kindYFMSpan, renderYFMSpan)
}

func renderYFMBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*yfmBlock)
	if entering {
		switch n.typ {
		case yfmNote:
			fmt.Fprintf(w, `<aside class="yfm-note yfm-note-%s">`, n.variant)
			if n.title != "" {
				fmt.Fprintf(w, `<div class="yfm-note-title">%s</div>`, template.HTMLEscapeString(n.title))
			}
			_, _ = w.WriteString(`<div class="yfm-note-content">`)
		case yfmCut:
			id := ""
			if n.id != "" {
				id = ` id="` + template.HTMLEscapeString(n.id) + `"`
			}
			group := ""
			if n.group != "" {
				group = ` data-group="` + template.HTMLEscapeString(n.group) + `"`
			}
			fmt.Fprintf(w, `<details class="yfm-cut"%s%s><summary>%s</summary><div class="yfm-cut-content">`, id, group, template.HTMLEscapeString(n.title))
		case yfmTabs:
			group := ""
			if n.group != "" {
				group = ` data-group="` + template.HTMLEscapeString(n.group) + `"`
			}
			fmt.Fprintf(w, `<div class="yfm-tabs"%s>`, group)
		case yfmTab:
			selected := ""
			if n.selected {
				selected = " data-selected"
			}
			fmt.Fprintf(w, `<section class="yfm-tab" data-title="%s"%s>`, template.HTMLEscapeString(n.title), selected)
		}
	} else {
		switch n.typ {
		case yfmNote:
			_, _ = w.WriteString("</div></aside>\n")
		case yfmCut:
			_, _ = w.WriteString("</div></details>\n")
		case yfmTabs:
			_, _ = w.WriteString("</div>\n")
		case yfmTab:
			_, _ = w.WriteString("</section>")
		}
	}
	return ast.WalkContinue, nil
}

func renderYFMSpan(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	tag := node.(*yfmSpan).tag
	if entering {
		_, _ = w.WriteString("<" + tag + ">")
	} else {
		_, _ = w.WriteString("</" + tag + ">")
	}
	return ast.WalkContinue, nil
}

type yfmTableCell struct {
	html             string
	align            string
	row, column      int
	rowspan, colspan int
}

type yfmTableRow struct {
	slots   []*yfmTableCell
	origins []*yfmTableCell
}

func renderYFMTable(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	headerRows, rawRows := parseYFMTable(node.(*yfmTable).body)
	rows := make([]yfmTableRow, 0, len(rawRows))
	for rowIndex, raw := range rawRows {
		row := yfmTableRow{}
		for column, value := range splitYFMCells(raw) {
			value, align := yfmCellAttrs(value)
			switch strings.TrimSpace(value) {
			case ">":
				if column > 0 && column-1 < len(row.slots) {
					cell := row.slots[column-1]
					if cell != nil {
						if cell.row == rowIndex {
							cell.colspan++
						}
						row.slots = append(row.slots, cell)
						continue
					}
				}
			case "^":
				if rowIndex > 0 && column < len(rows[rowIndex-1].slots) {
					cell := rows[rowIndex-1].slots[column]
					if cell != nil {
						cell.rowspan++
						row.slots = append(row.slots, cell)
						continue
					}
				}
			}
			cell := &yfmTableCell{html: renderYFMFragment(value), align: align, row: rowIndex, column: column, rowspan: 1, colspan: 1}
			row.slots = append(row.slots, cell)
			row.origins = append(row.origins, cell)
		}
		rows = append(rows, row)
	}

	_, _ = w.WriteString(`<table class="yfm-table"><tbody>`)
	for rowIndex, row := range rows {
		_, _ = w.WriteString("<tr>")
		for _, cell := range row.origins {
			tag := "td"
			if rowIndex < headerRows {
				tag = "th"
			}
			fmt.Fprintf(w, "<%s", tag)
			if tag == "th" {
				_, _ = w.WriteString(` scope="col"`)
			}
			if cell.rowspan > 1 {
				fmt.Fprintf(w, ` rowspan="%d"`, cell.rowspan)
			}
			if cell.colspan > 1 {
				fmt.Fprintf(w, ` colspan="%d"`, cell.colspan)
			}
			if cell.align != "" {
				fmt.Fprintf(w, ` class="yfm-align-%s"`, cell.align)
			}
			fmt.Fprintf(w, ">%s</%s>", cell.html, tag)
		}
		_, _ = w.WriteString("</tr>")
	}
	_, _ = w.WriteString("</tbody></table>\n")
	return ast.WalkSkipChildren, nil
}

var headerRowsAttr = regexp.MustCompile(`header-rows\s*=\s*"?(\d+)"?`)

func parseYFMTable(body string) (int, []string) {
	lines := strings.SplitAfter(body, "\n")
	headerRows := 0
	rows := []string{}
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "|:{") {
			if match := headerRowsAttr.FindStringSubmatch(trimmed); match != nil {
				headerRows, _ = strconv.Atoi(match[1])
			}
			i++
			continue
		}
		left := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(left, "||") {
			i++
			continue
		}
		var row strings.Builder
		part := left[2:]
		if strings.HasPrefix(part, ":{") {
			if end := strings.IndexByte(part, '}'); end >= 0 {
				part = part[end+1:]
			}
		}
		depth := 0
		for {
			line := part
			trim := strings.TrimSpace(line)
			if trim == "#|" {
				depth++
			} else if trim == "|#" && depth > 0 {
				depth--
			}
			if depth == 0 {
				withoutSpace := strings.TrimRight(line, " \t\r\n")
				if strings.HasSuffix(withoutSpace, "||") {
					row.WriteString(strings.TrimSuffix(withoutSpace, "||"))
					i++
					break
				}
			}
			row.WriteString(line)
			i++
			if i >= len(lines) {
				break
			}
			part = lines[i]
		}
		rows = append(rows, row.String())
	}
	return headerRows, rows
}

func splitYFMCells(row string) []string {
	lines := strings.SplitAfter(row, "\n")
	cells := []string{}
	var cell strings.Builder
	depth := 0
	fence := byte(0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "#|" {
			depth++
			cell.WriteString(line)
			continue
		}
		if trimmed == "|#" && depth > 0 {
			depth--
			cell.WriteString(line)
			continue
		}
		if depth > 0 {
			cell.WriteString(line)
			continue
		}
		plain := strings.TrimLeft(line, " \t")
		if len(plain) >= 3 && (strings.HasPrefix(plain, "```") || strings.HasPrefix(plain, "~~~")) {
			if fence == 0 {
				fence = plain[0]
			} else if fence == plain[0] {
				fence = 0
			}
			cell.WriteString(line)
			continue
		}
		if fence != 0 {
			cell.WriteString(line)
			continue
		}
		code := false
		for i := 0; i < len(line); i++ {
			if line[i] == '`' && (i == 0 || line[i-1] != '\\') {
				code = !code
			}
			if line[i] == '|' && !code && (i == 0 || line[i-1] != '\\') {
				cells = append(cells, strings.TrimSpace(cell.String()))
				cell.Reset()
				continue
			}
			cell.WriteByte(line[i])
		}
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	return cells
}

var alignAttr = regexp.MustCompile(`align\s*=\s*"(top-left|top-center|top-right|center|bottom-left|bottom-center|bottom-right)"`)

func yfmCellAttrs(value string) (string, string) {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if !strings.HasPrefix(trimmed, "::{") {
		return value, ""
	}
	end := strings.IndexByte(trimmed, '}')
	if end < 0 {
		return value, ""
	}
	align := ""
	if match := alignAttr.FindStringSubmatch(trimmed[:end+1]); match != nil {
		align = match[1]
	}
	return strings.TrimSpace(trimmed[end+1:]), align
}

func renderYFMFragment(source string) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	var buf bytes.Buffer
	ids := parser.WithIDs(&anchors{used: map[string]bool{}})
	if err := yfmMarkdown.Convert([]byte(source), &buf, parser.WithContext(parser.NewContext(ids))); err != nil {
		return "<pre>" + template.HTMLEscapeString(err.Error()) + "</pre>"
	}
	return buf.String()
}
