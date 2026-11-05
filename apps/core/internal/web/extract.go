package web

import (
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// skipped elements carry no readable page content.
var skipped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Svg: true,
	atom.Template: true, atom.Iframe: true, atom.Nav: true, atom.Footer: true,
	atom.Aside: true, atom.Select: true, atom.Button: true, atom.Head: true,
	atom.Canvas: true, atom.Object: true, atom.Embed: true, atom.Textarea: true,
	atom.Noembed: true, atom.Noframes: true,
}

// blocks start and end a line of text.
var blocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Br: true, atom.Li: true, atom.Ul: true, atom.Ol: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Tr: true, atom.Table: true, atom.Section: true, atom.Article: true, atom.Main: true,
	atom.Header: true, atom.Blockquote: true, atom.Pre: true, atom.Hr: true, atom.Dt: true,
	atom.Dd: true, atom.Figcaption: true, atom.Form: true, atom.Address: true,
}

// HTMLText returns the title and the readable text of an HTML document. The
// text of <main> or <article> wins when the page has one.
func HTMLText(r io.Reader) (title, text string, err error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", err
	}
	title = strings.Join(strings.Fields(textOf(findFirst(doc, atom.Title))), " ")
	root := findFirst(doc, atom.Main)
	if root == nil {
		root = findFirst(doc, atom.Article)
	}
	if root == nil {
		root = findFirst(doc, atom.Body)
	}
	if root == nil {
		root = doc
	}
	var b strings.Builder
	walk(&b, root, true)
	return title, tidy(b.String()), nil
}

func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findFirst(c, a); f != nil {
			return f
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

func walk(b *strings.Builder, n *html.Node, top bool) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		if !top && skipped[n.DataAtom] {
			return
		}
		if n.DataAtom == atom.Img {
			if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
				b.WriteString(" [图：" + alt + "] ")
			}
			return
		}
		if blocks[n.DataAtom] {
			b.WriteString("\n")
			defer b.WriteString("\n")
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(b, c, false)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// tidy collapses runs of spaces inside lines and runs of blank lines.
func tidy(s string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Truncate cuts s to at most n runes and reports whether it cut anything.
func Truncate(s string, n int) (string, bool) {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}
