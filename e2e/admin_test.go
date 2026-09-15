package e2e

import (
	"context"
	"fmt"
	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent/post"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// adminClient 回傳已登入 admin UI 的 http client（cookie session）。
func adminClient(t *testing.T, e *env, email string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.PostForm(e.ts.URL+"/admin/login", url.Values{
		"email": {email}, "password": {"password123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/admin" {
		t.Fatalf("login did not redirect to /admin (got %s)", resp.Request.URL.Path)
	}
	return c
}

func fetch(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

func TestAdminUI(t *testing.T) {
	e := setup(t)

	t.Run("RequiresLogin", func(t *testing.T) {
		noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := noRedirect.Get(e.ts.URL + "/admin")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/admin/login" {
			t.Errorf("unauthenticated /admin: %d → %s", resp.StatusCode, resp.Header.Get("Location"))
		}
	})

	t.Run("WrongPassword", func(t *testing.T) {
		c := &http.Client{}
		resp, err := c.PostForm(e.ts.URL+"/admin/login", url.Values{
			"email": {"editor@test.local"}, "password": {"nope"},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("wrong password: status %d, want 401", resp.StatusCode)
		}
	})

	t.Run("EditorSeesOwnPostsOnly", func(t *testing.T) {
		c := adminClient(t, e, "editor@test.local")
		status, body := fetch(t, c, e.ts.URL+"/admin/l/Post")
		if status != http.StatusOK {
			t.Fatalf("list view status %d", status)
		}
		if !strings.Contains(body, "editor 的已發布文章") {
			t.Error("editor's own post missing from list view")
		}
		if strings.Contains(body, "contributor 的草稿") {
			t.Error("row filter leaked another user's post into admin list view")
		}
	})

	t.Run("ContributorDeniedUserList", func(t *testing.T) {
		c := adminClient(t, e, "contributor@test.local")
		// 導覽不含 User，直接打 URL 也會被資料層擋下
		_, body := fetch(t, c, e.ts.URL+"/admin/l/User")
		if !strings.Contains(body, "access denied") {
			t.Error("contributor should see access denied on /admin/l/User")
		}
	})

	t.Run("CreateAndEditPost", func(t *testing.T) {
		c := adminClient(t, e, "moderator@test.local")
		resp, err := c.PostForm(e.ts.URL+"/admin/l/Post/new", url.Values{
			"title": {"admin UI 建立的文章"},
			"state": {"draft"},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// 成功會 redirect 到 /admin/l/Post/{id}（跟隨後為 200 表單頁）
		if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Request.URL.Path, "/admin/l/Post/") {
			t.Fatalf("create did not land on item page: %d %s", resp.StatusCode, resp.Request.URL.Path)
		}
		itemURL := resp.Request.URL.String()

		// 修改標題（帶 state 維持 select 值）
		resp2, err := c.PostForm(itemURL, url.Values{
			"title": {"admin UI 改過的標題"},
			"state": {"draft"},
		})
		if err != nil {
			t.Fatal(err)
		}
		var eb strings.Builder
		buf := make([]byte, 32*1024)
		for {
			n, err := resp2.Body.Read(buf)
			eb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		resp2.Body.Close()
		_, body := fetch(t, c, itemURL)
		if !strings.Contains(body, "admin UI 改過的標題") {
			t.Errorf("edited title not reflected on item page; update response contained: %s",
				extractErr(eb.String()))
		}
	})
}

// extractErr 從 HTML 中抓出 err div 內容（測試診斷用）。
func extractErr(html string) string {
	i := strings.Index(html, `class="err"`)
	if i < 0 {
		return "(no error div)"
	}
	j := strings.Index(html[i:], "</div>")
	if j < 0 {
		return html[i:]
	}
	return html[i : i+j]
}

// These checks exercise the actual GraphQL-backed UI against isolated PostgreSQL.
func TestNocturneAdmin(t *testing.T) {
	e := setup(t)
	admin := adminClient(t, e, "admin@test.local")
	editor := adminClient(t, e, "editor@test.local")
	contributor := adminClient(t, e, "contributor@test.local")
	t.Run("SearchAndFilterRespectOwnership", func(t *testing.T) {
		_, body := fetch(t, editor, e.ts.URL+"/admin/l/Post?filter=draft")
		if !strings.Contains(body, "沒有符合條件的資料") || strings.Contains(body, "contributor 的草稿") {
			t.Fatal("filter must apply within viewer's rows")
		}
		_, body = fetch(t, admin, e.ts.URL+"/admin/l/Post?q="+url.QueryEscape("contributor"))
		if !strings.Contains(body, "contributor 的草稿") || strings.Contains(body, "editor 的已發布文章") {
			t.Fatal("search did not filter records")
		}
	})
	t.Run("BulkPublishAndFieldPermission", func(t *testing.T) {
		id := fmt.Sprint(e.posts["contributor"].ID)
		resp, err := contributor.PostForm(e.ts.URL+"/admin/l/Post/bulk", url.Values{"ids": {id}, "action": {"published"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatal("contributor bulk publish must be rejected")
		}
		resp, err = editor.PostForm(e.ts.URL+"/admin/l/Post/bulk", url.Values{"ids": {id}, "action": {"draft"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		p, err := e.client.Post.Get(auth.WithSystem(context.Background()), e.posts["contributor"].ID)
		if err != nil || p.State != post.StateDraft {
			t.Fatal("unauthorized row changed")
		}
		resp, err = admin.PostForm(e.ts.URL+"/admin/l/Post/bulk", url.Values{"ids": {id}, "action": {"published"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		p, err = e.client.Post.Get(auth.WithSystem(context.Background()), e.posts["contributor"].ID)
		if err != nil || p.State != post.StatePublished || p.PublishTime.IsZero() {
			t.Fatalf("publish must set state and required time: %v", err)
		}
		resp, err = editor.PostForm(e.ts.URL+"/admin/l/Post/bulk", url.Values{"ids": {id}, "action": {"draft"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		p, err = e.client.Post.Get(auth.WithSystem(context.Background()), e.posts["contributor"].ID)
		if err != nil || p.State != post.StatePublished {
			t.Fatal("editor must not update another owner's post through bulk actions")
		}

	})
	t.Run("InvalidSaveRetainsRichTextAndTitle", func(t *testing.T) {
		form := url.Values{"title": {"保留未儲存的標題"}, "state": {"published"}, "content": {`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"不要遺失內文"}]}]}`}}
		resp, err := admin.PostForm(e.ts.URL+"/admin/l/Post/new", form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body := string(b)
		if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "保留未儲存的標題") || !strings.Contains(body, "不要遺失內文") {
			t.Fatal("failed save must retain submitted text")
		}
	})
	t.Run("CreateUserWithPassword", func(t *testing.T) {
		resp, err := admin.PostForm(e.ts.URL+"/admin/l/User/new", url.Values{"name": {"New editor"}, "email": {"new@test.local"}, "role": {"editor"}, "password": {"password123"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		adminClient(t, e, "new@test.local")
	})
	t.Run("OptionalFieldsCanBeCleared", func(t *testing.T) {
		sys := auth.WithSystem(context.Background())
		p, err := e.client.Post.Create().SetTitle("Clear fields").SetSubtitle("subtitle").SetContent(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}).SetCreatedBy(e.users["admin"]).Save(sys)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := admin.PostForm(fmt.Sprintf("%s/admin/l/Post/%d", e.ts.URL, p.ID), url.Values{"title": {"Clear fields"}, "subtitle": {""}, "content": {""}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		p, err = e.client.Post.Get(sys, p.ID)
		if err != nil || p.Subtitle != "" || p.Content != nil {
			t.Fatal("cleared fields must persist")
		}
	})
	t.Run("AllListsRenderValidText", func(t *testing.T) {
		_, err := e.client.Tag.Create().SetName("中文標籤").Save(auth.WithSystem(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		for _, list := range []string{"Post", "Author", "Category", "Photo", "Section", "Tag", "User"} {
			status, body := fetch(t, admin, e.ts.URL+"/admin/l/"+list)
			if status != 200 || !utf8.ValidString(body) || strings.Contains(body, "無法完成操作") {
				t.Fatalf("invalid list page: %s", list)
			}
		}
	})

	t.Run("CursorPaginationKeepsSearch", func(t *testing.T) {
		for i := 0; i < 23; i++ {
			_, err := e.client.Post.Create().SetTitle(fmt.Sprintf("Pagination %02d", i)).SetCreatedBy(e.users["editor"]).Save(auth.WithSystem(context.Background()))
			if err != nil {
				t.Fatal(err)
			}
		}
		_, body := fetch(t, editor, e.ts.URL+"/admin/l/Post?q=Pagination")
		re := regexp.MustCompile(`aria-label="下一頁" href="([^"]+)"`)
		m := re.FindStringSubmatch(body)
		if len(m) != 2 {
			t.Fatal("missing next page")
		}
		next := html.UnescapeString(m[1])
		if !strings.Contains(next, "q=Pagination") {
			t.Fatal("pagination lost search")
		}
		_, body = fetch(t, editor, e.ts.URL+next)
		if !strings.Contains(body, "本頁 3 筆") || !strings.Contains(body, `aria-label="上一頁" href=`) {
			t.Fatal("second page incorrect")
		}
	})
}
