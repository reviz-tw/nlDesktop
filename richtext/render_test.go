package richtext

import (
	"strings"
	"testing"
)

func TestWebsiteHTML(t *testing.T) {
	d := doc(
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2, "textAlign": "center"}, "content": []any{map[string]any{"type": "text", "text": "標題 <test>"}}},
		para("淨零排放", "bold"),
		map[string]any{"type": "infoBox", "attrs": map[string]any{"title": "補充資訊"}, "content": []any{para("重點")}},
		map[string]any{"type": "slideshow", "attrs": map[string]any{"photoIds": []int{7, 8}, "caption": "現場照片"}},
	)
	got, err := Render(d, map[int]Photo{7: {URL: "https://example.com/7.jpg", Alt: "照片七"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<h2 class="article-align-center">標題 &lt;test&gt;</h2>`, `<strong>淨零排放</strong>`, `<aside class="article-info-box">`, `<figcaption>現場照片</figcaption>`, `src="https://example.com/7.jpg"`, `alt="照片七"`, `data-photo-id="8">圖片無法顯示`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "style=") {
		t.Fatal("website theme must not be embedded in content HTML")
	}
}

func TestWebsiteHTMLDoesNotTrustAttributes(t *testing.T) {
	d := doc(
		map[string]any{"type": "paragraph", "attrs": map[string]any{"textAlign": `center" onclick="bad()`}, "content": []any{map[string]any{"type": "text", "text": "<script>bad()</script>", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": "javascript:alert(1)"}}}}}},
		map[string]any{"type": "image", "attrs": map[string]any{"src": "javascript:alert(1)", "onerror": "bad()"}},
		map[string]any{"type": "image", "attrs": map[string]any{"src": "https://example.com/a.jpg", "alt": `" onerror="bad()`}},
		map[string]any{"type": "embed", "attrs": map[string]any{"html": `<script>parent.document.body.innerHTML='bad'</script>`}},
	)
	got, err := Render(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script>") || strings.Contains(got, `href="javascript:`) || strings.Contains(got, `src="javascript:`) || strings.Contains(got, ` onclick=`) || strings.Contains(got, `" onerror="`) {
		t.Fatalf("unsafe markup: %s", got)
	}
	if !strings.Contains(got, `sandbox="allow-scripts"`) || strings.Contains(got, "allow-same-origin") {
		t.Fatal("raw embeds must remain in an opaque sandbox")
	}
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,<script>", "//evil.example", "/\\evil.example", "https://user:password@example.com", "java\nscript:alert(1)"} {
		if safeURL(raw, true) != "" {
			t.Errorf("unsafe URL accepted: %q", raw)
		}
	}
}

func TestWebsiteRendererCoverage(t *testing.T) {
	cases := map[string]map[string]any{}
	for name := range allowedNodes {
		cases[name] = map[string]any{"type": name}
	}
	cases["text"]["text"] = "text"
	cases["heading"]["attrs"] = map[string]any{"level": 2}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			d := doc(node)
			if name == "doc" {
				d = node
			}
			if _, err := Render(d, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	for mark := range allowedMarks {
		if _, err := Render(doc(para("format", mark)), nil); err != nil {
			t.Fatalf("mark %s: %v", mark, err)
		}
	}
	if out, err := Render(nil, nil); out != "" || err != nil {
		t.Fatal("empty content must render as empty HTML")
	}
	if _, err := Render(doc(map[string]any{"type": "script"}), nil); err == nil {
		t.Fatal("unknown node accepted")
	}
	if _, err := Render(map[string]any{"type": "doc", "content": "invalid"}, nil); err == nil {
		t.Fatal("invalid structure accepted")
	}
	nested := doc(para("deep"))
	for i := 0; i < 102; i++ {
		nested = doc(nested)
	}
	if _, err := Render(nested, nil); err == nil {
		t.Fatal("excessive nesting accepted")
	}
}

func TestYoutubeCanonicalURL(t *testing.T) {
	for _, source := range []string{"https://youtu.be/dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"} {
		got, err := Render(doc(map[string]any{"type": "youtube", "attrs": map[string]any{"src": source}}), nil)
		if err != nil || !strings.Contains(got, `src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`) {
			t.Fatalf("invalid video: %s %v", got, err)
		}
	}
	if youtubeSource("https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ") != "" {
		t.Fatal("unexpected video host accepted")
	}
}
