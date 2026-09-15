package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/nltest"
	"github.com/hcchien/nl/postrender"
	"github.com/hcchien/nl/richtext"
)

func articleDoc(text string) map[string]any {
	return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}
}

func TestPersistedContentHTML(t *testing.T) {
	e := setup(t)
	sys := auth.WithSystem(context.Background())
	p, err := e.client.Post.Create().SetTitle("Website output").SetContent(articleDoc("第一版")).SetCreatedBy(e.users["editor"]).Save(sys)
	if err != nil {
		t.Fatal(err)
	}
	if p.ContentHTML != "<p>第一版</p>" || p.RenderVersion != richtext.RenderVersion {
		t.Fatalf("create did not derive HTML: %+v", p)
	}
	t.Run("GraphQLReadAndUpdate", func(t *testing.T) {
		res := e.gql(t, "editor", `query($id:ID){posts(where:{id:$id}){edges{node{contentHtml renderVersion}}}}`, map[string]any{"id": p.ID})
		res.mustNoErrors(t)
		var result struct {
			Edges []struct {
				Node struct {
					ContentHtml   string
					RenderVersion int
				}
			}
		}
		if err := json.Unmarshal(res.Data["posts"], &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Edges) != 1 || result.Edges[0].Node.ContentHtml != "<p>第一版</p>" {
			t.Fatalf("wrong GraphQL content: %s", res.Data["posts"])
		}
		res = e.gql(t, "editor", `mutation($id:ID!,$input:UpdatePostInput!){updatePost(id:$id,input:$input){contentHtml renderVersion}}`, map[string]any{"id": p.ID, "input": map[string]any{"content": articleDoc("第二版")}})
		res.mustNoErrors(t)
		updated, _ := e.client.Post.Get(sys, p.ID)
		if updated.ContentHTML != "<p>第二版</p>" {
			t.Fatal("update did not regenerate HTML")
		}
	})
	t.Run("ReadOnlyAndPermissions", func(t *testing.T) {
		res := e.gql(t, "admin", `mutation($id:ID!){updatePost(id:$id,input:{contentHtml:"injected"}){id}}`, map[string]any{"id": p.ID})
		if len(res.Errors) == 0 {
			t.Fatal("derived HTML is present in mutation input")
		}
		res = e.gql(t, "contributor", `query($id:ID){posts(where:{id:$id}){edges{node{contentHtml}}}}`, map[string]any{"id": p.ID})
		res.mustNoErrors(t)
		if strings.Contains(string(res.Data["posts"]), "第二版") {
			t.Fatal("rendered HTML bypassed ownership")
		}
		if _, err := e.client.Post.UpdateOneID(p.ID).SetContentHTML("injected").Save(sys); err == nil {
			t.Fatal("direct mutation bypassed derived-field hook")
		}
		if _, err := e.client.Post.UpdateOneID(p.ID).AddRenderVersion(10).Save(sys); err == nil {
			t.Fatal("version can be externally incremented")
		}
	})
	t.Run("InvalidRenderDoesNotCommitJSON", func(t *testing.T) {
		malformed := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "heading", "attrs": map[string]any{"level": 9}}}}
		if _, err := e.client.Post.UpdateOneID(p.ID).SetContent(malformed).Save(sys); err == nil {
			t.Fatal("invalid render accepted")
		}
		current, _ := e.client.Post.Get(sys, p.ID)
		if current.ContentHTML != "<p>第二版</p>" {
			t.Fatal("failed render changed saved output")
		}
		source, _ := json.Marshal(current.Content)
		if !strings.Contains(string(source), "第二版") {
			t.Fatal("JSON committed without corresponding HTML")
		}
	})
	t.Run("ContentClearAndMetadataUpdate", func(t *testing.T) {
		before, _ := e.client.Post.Get(sys, p.ID)
		after, err := e.client.Post.UpdateOneID(p.ID).SetTitle("New title").Save(sys)
		if err != nil || after.ContentHTML != before.ContentHTML {
			t.Fatal("metadata update changed HTML")
		}
		after, err = e.client.Post.UpdateOneID(p.ID).ClearContent().Save(sys)
		if err != nil || after.ContentHTML != "" || after.RenderVersion != richtext.RenderVersion {
			t.Fatal("clear left stale HTML")
		}
	})
	t.Run("ReferencedPhotosRenderOnServer", func(t *testing.T) {
		ph, err := e.client.Photo.Create().SetName("現場").SetURL("https://example.com/photo.jpg").Save(sys)
		if err != nil {
			t.Fatal(err)
		}
		d := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "slideshow", "attrs": map[string]any{"photoIds": []int{ph.ID}}}}}
		after, err := e.client.Post.UpdateOneID(p.ID).SetContent(d).Save(sys)
		if err != nil || !strings.Contains(after.ContentHTML, `src="https://example.com/photo.jpg"`) {
			t.Fatalf("slideshow lacks rendered image: %v", err)
		}
	})
}

func TestRebuildContentHTML(t *testing.T) {
	e := setup(t)
	sys := auth.WithSystem(context.Background())
	p, err := e.client.Post.Create().SetTitle("Legacy").SetContent(articleDoc("舊文章")).Save(sys)
	if err != nil {
		t.Fatal(err)
	}
	p, _ = e.client.Post.Get(sys, p.ID)
	dbURL := os.Getenv("NL_TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = nltest.DefaultDatabaseURL
	}
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(sys, "UPDATE posts SET content_html = NULL, render_version = 0 WHERE id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	result, err := postrender.Rebuild(sys, e.client, false)
	if err != nil || result.Updated != 1 {
		t.Fatalf("rebuild: %+v %v", result, err)
	}
	after, _ := e.client.Post.Get(sys, p.ID)
	if after.ContentHTML != "<p>舊文章</p>" || !after.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatal("backfill must preserve editorial timestamp and render the legacy document")
	}
	result, err = postrender.Rebuild(sys, e.client, false)
	if err != nil || result.Updated != 0 {
		t.Fatal("up-to-date records should be skipped")
	}
	if _, err := postrender.Rebuild(context.Background(), e.client, true); err == nil {
		t.Fatal("untrusted rebuild allowed")
	}
	// Inject a real concurrent edit immediately before the guarded backfill write.
	didEdit := false
	e.client.Post.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			pm := m.(*ent.PostMutation)
			if _, hasHTML := pm.ContentHTML(); hasHTML && m.Op().Is(ent.OpUpdate) && !didEdit {
				didEdit = true
				if _, err := e.client.Post.UpdateOneID(p.ID).SetContent(articleDoc("同時編輯的新內容")).Save(sys); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	result, err = postrender.Rebuild(sys, e.client, true)
	if err != nil {
		t.Fatal(err)
	}
	// Other fixture posts can be visited first; p is read into the same batch.
	after, _ = e.client.Post.Get(sys, p.ID)
	if !didEdit || result.Skipped < 1 || after.ContentHTML != "<p>同時編輯的新內容</p>" {
		t.Fatal(fmt.Sprintf("backfill overwrote concurrent edit: %+v %s", result, after.ContentHTML))
	}
}
