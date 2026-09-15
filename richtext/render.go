package richtext

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// RenderVersion changes when the HTML structure or escaping policy changes.
// Site CSS changes do not require a renderer version bump.
const RenderVersion = 1

// Photo is a snapshot of a CMS image used when producing website markup.
type Photo struct{ URL, Alt string }

// Render creates website HTML without a browser or an editor runtime. It only
// emits known semantic elements and attributes. Theme styling belongs to CSS.
// photos contains only images the rendering caller is authorized to read.
func Render(doc map[string]any, photos map[int]Photo) (string, error) {
	if doc == nil {
		return "", nil
	}
	// Normalize maps/slices supplied by Go callers as well as decoded GraphQL JSON.
	data, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if len(data) > 10<<20 {
		return "", fmt.Errorf("richtext: document exceeds 10 MiB")
	}
	var normalized map[string]any
	if err = json.Unmarshal(data, &normalized); err != nil {
		return "", err
	}
	if normalized["type"] != "doc" {
		return "", fmt.Errorf("richtext: root must be doc")
	}
	r := htmlRenderer{photos: photos}
	return r.node(normalized, 0)
}

type htmlRenderer struct{ photos map[int]Photo }

var languageName = regexp.MustCompile(`^[a-zA-Z0-9_+.-]{1,40}$`)
var youtubeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{11}$`)

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func integer(v any) int {
	switch n := v.(type) {
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
	case int:
		return n
	case json.Number:
		i, _ := strconv.Atoi(string(n))
		return i
	}
	return 0
}
func escape(s string) string { return html.EscapeString(s) }

// URL values are escaped separately after protocol validation. No arbitrary
// event handlers, styles, class names, or executable URLs are copied to markup.
func safeURL(raw string, link bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, `\`) || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Hostname() == "" || u.User != nil {
			return ""
		}
		return raw
	case "mailto", "tel":
		if link {
			return raw
		}
	case "":
		if u.Host == "" && !strings.HasPrefix(raw, "//") {
			return raw
		}
	}
	return ""
}
func alignment(attrs map[string]any) string {
	switch str(attrs, "textAlign") {
	case "left", "right", "center", "justify":
		return ` class="article-align-` + str(attrs, "textAlign") + `"`
	}
	return ""
}
func (r htmlRenderer) node(n map[string]any, depth int) (string, error) {
	if depth > 100 {
		return "", fmt.Errorf("richtext: nesting exceeds 100 levels")
	}
	typ := str(n, "type")
	if _, ok := allowedNodes[typ]; !ok {
		return "", fmt.Errorf("richtext: unsupported node %q", typ)
	}
	attrs, _ := n["attrs"].(map[string]any)
	var children strings.Builder
	if raw, exists := n["content"]; exists {
		nodes, ok := raw.([]any)
		if !ok {
			return "", fmt.Errorf("richtext: content must be an array")
		}
		for _, rawNode := range nodes {
			child, ok := rawNode.(map[string]any)
			if !ok {
				return "", fmt.Errorf("richtext: invalid child")
			}
			out, err := r.node(child, depth+1)
			if err != nil {
				return "", err
			}
			children.WriteString(out)
		}
	}
	body := children.String()
	switch typ {
	case "doc":
		return body, nil
	case "text":
		if _, ok := n["text"].(string); !ok {
			return "", fmt.Errorf("richtext: text must be a string")
		}
		body = escape(str(n, "text"))
		if raw, exists := n["marks"]; exists {
			marks, ok := raw.([]any)
			if !ok {
				return "", fmt.Errorf("richtext: marks must be an array")
			}
			for i := len(marks) - 1; i >= 0; i-- {
				mark, ok := marks[i].(map[string]any)
				if !ok {
					return "", fmt.Errorf("richtext: invalid mark")
				}
				var err error
				body, err = renderMark(body, mark)
				if err != nil {
					return "", err
				}
			}
		}
		return body, nil
	case "paragraph":
		return "<p" + alignment(attrs) + ">" + body + "</p>", nil
	case "heading":
		level := integer(attrs["level"])
		if level < 1 || level > 6 {
			return "", fmt.Errorf("richtext: heading level must be 1–6")
		}
		tag := "h" + strconv.Itoa(level)
		return "<" + tag + alignment(attrs) + ">" + body + "</" + tag + ">", nil
	case "blockquote":
		return "<blockquote>" + body + "</blockquote>", nil
	case "bulletList":
		return "<ul>" + body + "</ul>", nil
	case "orderedList":
		start := ""
		if v := integer(attrs["start"]); v > 1 {
			start = ` start="` + strconv.Itoa(v) + `"`
		}
		return "<ol" + start + ">" + body + "</ol>", nil
	case "listItem":
		return "<li>" + body + "</li>", nil
	case "codeBlock":
		class := ""
		if lang := str(attrs, "language"); languageName.MatchString(lang) {
			class = ` class="language-` + escape(lang) + `"`
		}
		return "<pre><code" + class + ">" + body + "</code></pre>", nil
	case "horizontalRule":
		return "<hr>", nil
	case "hardBreak":
		return "<br>", nil
	case "image":
		return r.image(attrs), nil
	case "infoBox":
		title := ""
		if str(attrs, "title") != "" {
			title = `<p class="article-info-box-title">` + escape(str(attrs, "title")) + `</p>`
		}
		return `<aside class="article-info-box">` + title + body + `</aside>`, nil
	case "slideshow":
		var figures strings.Builder
		if values, ok := attrs["photoIds"].([]any); ok {
			for _, value := range values {
				if id := integer(value); id > 0 {
					figures.WriteString(`<figure class="article-slide">` + r.image(map[string]any{"photoId": id}) + `</figure>`)
				}
			}
		}
		caption := ""
		if str(attrs, "caption") != "" {
			caption = "<figcaption>" + escape(str(attrs, "caption")) + "</figcaption>"
		}
		return `<figure class="article-slideshow" data-type="slideshow"><div class="article-slides">` + figures.String() + `</div>` + caption + `</figure>`, nil
	case "youtube":
		src := youtubeSource(str(attrs, "src"))
		if src == "" {
			return `<p class="article-media-unavailable">影片無法顯示</p>`, nil
		}
		if start := integer(attrs["start"]); start > 0 {
			src += "?start=" + strconv.Itoa(start)
		}
		return `<figure class="article-video"><iframe src="` + escape(src) + `" title="YouTube 影片" loading="lazy" referrerpolicy="no-referrer" allow="fullscreen; picture-in-picture" allowfullscreen></iframe></figure>`, nil
	case "embed":
		// Raw embeds never execute in the article origin. In particular, do not add
		// allow-same-origin: srcdoc must retain its opaque, sandboxed origin.
		caption := ""
		if str(attrs, "caption") != "" {
			caption = "<figcaption>" + escape(str(attrs, "caption")) + "</figcaption>"
		}
		return `<figure class="article-embed"><iframe title="嵌入內容" sandbox="allow-scripts" loading="lazy" referrerpolicy="no-referrer" srcdoc="` + escape(str(attrs, "html")) + `"></iframe>` + caption + `</figure>`, nil
	}
	return "", fmt.Errorf("richtext: missing renderer for %q", typ)
}
func renderMark(body string, mark map[string]any) (string, error) {
	typ := str(mark, "type")
	attrs, _ := mark["attrs"].(map[string]any)
	tags := map[string]string{"bold": "strong", "italic": "em", "strike": "s", "code": "code", "underline": "u", "subscript": "sub", "superscript": "sup", "textStyle": "span"}
	if tag, ok := tags[typ]; ok {
		return "<" + tag + ">" + body + "</" + tag + ">", nil
	}
	if typ == "link" {
		href := safeURL(str(attrs, "href"), true)
		if href == "" {
			return body, nil
		}
		target := ""
		if str(attrs, "target") == "_blank" {
			target = ` target="_blank" rel="noopener noreferrer"`
		}
		return `<a href="` + escape(href) + `"` + target + `>` + body + `</a>`, nil
	}
	return "", fmt.Errorf("richtext: unsupported mark %q", typ)
}
func (r htmlRenderer) image(attrs map[string]any) string {
	id := integer(attrs["photoId"])
	src, alt := str(attrs, "src"), str(attrs, "alt")
	if photo, ok := r.photos[id]; ok {
		src = photo.URL
		if alt == "" {
			alt = photo.Alt
		}
	}
	src = safeURL(src, false)
	photoAttr := ""
	if id > 0 {
		photoAttr = ` data-photo-id="` + strconv.Itoa(id) + `"`
	}
	if src == "" {
		return `<span class="article-media-unavailable"` + photoAttr + `>圖片無法顯示</span>`
	}
	title := ""
	if t := str(attrs, "title"); t != "" {
		title = ` title="` + escape(t) + `"`
	}
	return `<img src="` + escape(src) + `" alt="` + escape(alt) + `"` + title + photoAttr + ` loading="lazy" decoding="async">`
}
func youtubeSource(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	id := ""
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "www.youtube.com", "m.youtube.com", "youtube-nocookie.com", "www.youtube-nocookie.com":
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) == 2 && (parts[0] == "embed" || parts[0] == "shorts") {
				id = parts[1]
			}
		}
	}
	if !youtubeID.MatchString(id) {
		return ""
	}
	return "https://www.youtube-nocookie.com/embed/" + id
}

// PhotoIDs returns distinct valid references used by image/slideshow nodes.
func PhotoIDs(doc map[string]any) []int {
	data, _ := json.Marshal(doc)
	var n map[string]any
	json.Unmarshal(data, &n)
	seen := map[int]bool{}
	var ids []int
	var walk func(map[string]any)
	add := func(v any) {
		if id := integer(v); id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	walk = func(n map[string]any) {
		attrs, _ := n["attrs"].(map[string]any)
		switch str(n, "type") {
		case "image":
			add(attrs["photoId"])
		case "slideshow":
			values, _ := attrs["photoIds"].([]any)
			for _, v := range values {
				add(v)
			}
		}
		children, _ := n["content"].([]any)
		for _, v := range children {
			if child, ok := v.(map[string]any); ok {
				walk(child)
			}
		}
	}
	walk(n)
	return ids
}
