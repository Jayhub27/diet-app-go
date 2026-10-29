package templates

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"
)

// FormatMessage renders the small subset of Markdown that chat models actually
// emit (bold, italics, inline code, links, headings, bullet/numbered lists and
// paragraphs). The input is escaped first, so the result is safe to inject.
//
// This is deliberately not a full Markdown parser: the app has no JS build
// step and no room for a dependency, and model replies are short.
func FormatMessage(raw string) template.HTML {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if text == "" {
		return ""
	}

	var b strings.Builder
	var paragraph []string
	var list strings.Builder
	listType := ""

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		b.WriteString("<p>")
		b.WriteString(strings.Join(paragraph, "<br>"))
		b.WriteString("</p>")
		paragraph = paragraph[:0]
	}
	flushList := func() {
		if list.Len() == 0 {
			return
		}
		b.WriteString("<" + listType + ">")
		b.WriteString(list.String())
		b.WriteString("</" + listType + ">")
		list.Reset()
		listType = ""
	}
	startList := func(kind string) {
		if listType != kind {
			flushList()
			listType = kind
		}
	}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flushParagraph()
			flushList()
		case reHeading.MatchString(line):
			flushParagraph()
			flushList()
			m := reHeading.FindStringSubmatch(line)
			level := len(m[1])
			if level > 3 {
				level = 3
			}
			fmt.Fprintf(&b, "<h%d>%s</h%d>", level, inline(html.EscapeString(m[2])), level)
		case reBullet.MatchString(line):
			flushParagraph()
			startList("ul")
			list.WriteString("<li>" + inline(html.EscapeString(reBullet.FindStringSubmatch(line)[1])) + "</li>")
		case reNumbered.MatchString(line):
			flushParagraph()
			startList("ol")
			list.WriteString("<li>" + inline(html.EscapeString(reNumbered.FindStringSubmatch(line)[1])) + "</li>")
		default:
			flushList()
			paragraph = append(paragraph, inline(html.EscapeString(line)))
		}
	}
	flushParagraph()
	flushList()
	return template.HTML(b.String())
}

var (
	reHeading  = regexp.MustCompile(`^(#{1,3})\s+(.+)$`)
	reBullet   = regexp.MustCompile(`^[-*•]\s+(.+)$`)
	reNumbered = regexp.MustCompile(`^\d{1,2}[.)]\s+(.+)$`)
	reLink     = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+)\)`)
	reBold     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic   = regexp.MustCompile(`\*([^\s*][^*\n]*?)\*`)
)

// inline formats one already-escaped line. Code spans are swapped out first so
// **inside** them stays literal.
func inline(escaped string) string {
	var codes []string
	for {
		i := strings.IndexByte(escaped, '`')
		if i < 0 {
			break
		}
		j := strings.IndexByte(escaped[i+1:], '`')
		if j < 0 {
			break
		}
		codes = append(codes, escaped[i+1:i+1+j])
		escaped = escaped[:i] + fmt.Sprintf("\x00%d\x00", len(codes)-1) + escaped[i+j+2:]
	}

	escaped = reBold.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = reItalic.ReplaceAllString(escaped, "<em>$1</em>")
	escaped = reLink.ReplaceAllString(escaped, `<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>`)

	for i, code := range codes {
		escaped = strings.ReplaceAll(escaped, fmt.Sprintf("\x00%d\x00", i), "<code>"+code+"</code>")
	}
	return escaped
}
