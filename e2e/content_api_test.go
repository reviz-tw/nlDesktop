package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/apikey"
	"github.com/hcchien/nl/ent/post"
	"github.com/hcchien/nl/ent/user"
)

func (e *env) contentRequest(t *testing.T, token, query string) (int, *gqlResp) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req, _ := http.NewRequest("POST", e.ts.URL+"/graphql/content", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("content response may be shared-cached")
	}
	var out gqlResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, &out
}

func TestContentAPI(t *testing.T) {
	e := setup(t)
	sys := auth.WithSystem(context.Background())
	issue := e.gql(t, "admin", `mutation{createContentApiKey(name:"website test"){id name key}}`, nil)
	issue.mustNoErrors(t)
	var key struct{ ID, Name, Key string }
	if err := json.Unmarshal(issue.Data["createContentApiKey"], &key); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"", "editor", "contributor"} {
		if !e.gql(t, role, `mutation{createContentApiKey(name:"denied"){key}}`, nil).hasErrorContaining("access denied") {
			t.Fatalf("%s issued content key", role)
		}
	}
	regular := e.gql(t, "admin", `mutation{createApiKey(name:"regular"){key}}`, nil)
	regular.mustNoErrors(t)
	var cmsKey struct{ Key string }
	json.Unmarshal(regular.Data["createApiKey"], &cmsKey)

	for _, token := range []string{"", "bad", key.Key + "x", e.tokens["admin"], cmsKey.Key} {
		status, _ := e.contentRequest(t, token, `{posts{totalCount}}`)
		if status != 401 {
			t.Fatalf("wrong credentials accepted: status %d", status)
		}
	}
	e.tokens["website"] = key.Key
	for _, query := range []string{postsQuery, `{users{totalCount}}`, `mutation{createApiKey(name:"escalate"){key}}`, `mutation{createPost(input:{title:"forbidden"}){id}}`} {
		r := e.gql(t, "website", query, nil)
		if len(r.Errors) == 0 {
			t.Fatalf("website key accessed CMS: %s", query)
		}
	}

	privateIDs := []int{e.posts["contributor"].ID}
	for _, state := range []post.State{post.StateScheduled, post.StateInvisible, post.StateArchived} {
		p := e.client.Post.Create().SetTitle("PRIVATE " + string(state)).SetState(state).SetPublishTime(time.Now().Add(-time.Hour)).SaveX(sys)
		privateIDs = append(privateIDs, p.ID)
	}
	future := e.client.Post.Create().SetTitle("PRIVATE future").SetState(post.StatePublished).SetPublishTime(time.Now().Add(time.Hour)).SaveX(sys)
	undated := e.client.Post.Create().SetTitle("PRIVATE undated").SetState(post.StatePublished).SaveX(sys)
	privateIDs = append(privateIDs, future.ID, undated.ID)

	publicPhoto := e.client.Photo.Create().SetName("public hero").SetURL("https://example.com/hero.jpg").SaveX(sys)
	portrait := e.client.Photo.Create().SetName("public portrait").SetURL("https://example.com/portrait.jpg").SaveX(sys)
	privatePhoto := e.client.Photo.Create().SetName("PRIVATE photo").SetURL("https://example.com/private.jpg").SaveX(sys)
	writer := e.client.Author.Create().SetName("public writer").SetImage(portrait).SaveX(sys)
	privateWriter := e.client.Author.Create().SetName("PRIVATE writer").SetImage(privatePhoto).SaveX(sys)
	section := e.client.Section.Create().SetSlug("public").SetName("public section").SaveX(sys)
	categorySection := e.client.Section.Create().SetSlug("viaCategory").SetName("category section").SaveX(sys)
	privateSection := e.client.Section.Create().SetSlug("private").SetName("PRIVATE section").SaveX(sys)
	category := e.client.Category.Create().SetSlug("public").SetName("public category").SetSection(categorySection).SaveX(sys)
	privateCategory := e.client.Category.Create().SetSlug("private").SetName("PRIVATE category").SetSection(privateSection).SaveX(sys)
	tag := e.client.Tag.Create().SetName("public tag").SaveX(sys)
	privateTag := e.client.Tag.Create().SetName("PRIVATE tag").SaveX(sys)
	related := e.client.Post.Create().SetTitle("public related").SetState(post.StatePublished).SetPublishTime(time.Now().Add(-time.Hour)).SaveX(sys)
	e.client.Post.UpdateOne(e.posts["editor"]).SetHeroImage(publicPhoto).SetSection(section).AddCategories(category).AddTags(tag).AddWriters(writer).AddRelatedPostIDs(append(privateIDs, related.ID)...).
		SetContent(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "public HTML"}}}}}).SaveX(sys)
	e.client.Post.UpdateOne(e.posts["contributor"]).SetHeroImage(privatePhoto).SetSection(privateSection).AddCategories(privateCategory).AddTags(privateTag).AddWriters(privateWriter).SaveX(sys)

	t.Run("PublicProjectionAndRelations", func(t *testing.T) {
		_, r := e.contentRequest(t, key.Key, `{posts{totalCount hasMore items{id title contentHtml renderVersion publishTime heroImage{url} section{name} categories{name section{name}} tags{name} writers{name image{url}} relatedPosts{title}}} sections{name} categories{name} tags{name}}`)
		r.mustNoErrors(t)
		b, _ := json.Marshal(r.Data)
		for _, expected := range []string{`"totalCount":2`, `public HTML`, `hero.jpg`, `portrait.jpg`, `public writer`, `public category`, `category section`, `public related`} {
			if !strings.Contains(string(b), expected) {
				t.Errorf("missing %q in %s", expected, b)
			}
		}
		if strings.Contains(string(b), "PRIVATE") || strings.Contains(string(b), "草稿") {
			t.Fatalf("private data leaked: %s", b)
		}
		_, page := e.contentRequest(t, key.Key, `{posts(first:1){totalCount hasMore items{id}}}`)
		page.mustNoErrors(t)
		if !strings.Contains(string(page.Data["posts"]), `"hasMore":true`) {
			t.Fatal("pagination incorrect")
		}
		_, filtered := e.contentRequest(t, key.Key, `{posts(sectionSlug:"private"){totalCount items{id}}}`)
		filtered.mustNoErrors(t)
		if !strings.Contains(string(filtered.Data["posts"]), `"totalCount":0`) {
			t.Fatal("filter exposed private posts")
		}
		_, filtered = e.contentRequest(t, key.Key, fmt.Sprintf(`{posts(sectionSlug:"public",categorySlug:"public",tagId:"%d"){totalCount}}`, tag.ID))
		filtered.mustNoErrors(t)
		if !strings.Contains(string(filtered.Data["posts"]), `"totalCount":1`) {
			t.Fatal("public filters incorrect")
		}
		_, next := e.contentRequest(t, key.Key, `{posts(first:1,offset:1){hasMore items{title}}}`)
		next.mustNoErrors(t)
		if !strings.Contains(string(next.Data["posts"]), `public related`) || !strings.Contains(string(next.Data["posts"]), `"hasMore":false`) {
			t.Fatal("next page incorrect")
		}
	})
	t.Run("PrivateIDsAndSchema", func(t *testing.T) {
		for _, id := range append(privateIDs, 999999) {
			_, r := e.contentRequest(t, key.Key, fmt.Sprintf(`{post(id:"%d"){id contentHtml}}`, id))
			r.mustNoErrors(t)
			if string(r.Data["post"]) != "null" {
				t.Fatalf("private ID %d visible: %s", id, r.Data["post"])
			}
		}
		for _, query := range []string{`{users{id email}}`, `{node(id:"1"){id}}`, `{post(id:"1"){content createdBy{id} state}}`, `mutation{createPost(input:{title:"no"}){id}}`, `{posts(first:51){totalCount}}`, `{posts(offset:-1){totalCount}}`} {
			_, r := e.contentRequest(t, key.Key, query)
			if len(r.Errors) == 0 {
				t.Fatalf("forbidden query accepted: %s", query)
			}
		}
		_, r := e.contentRequest(t, key.Key, `{__schema{mutationType{name}} __type(name:"User"){name}}`)
		r.mustNoErrors(t)
		if string(r.Data["__type"]) != "null" || !strings.Contains(string(r.Data["__schema"]), `"mutationType":null`) {
			t.Fatal("CMS schema present")
		}
		query := "{"
		for i := 0; i < 45; i++ {
			query += fmt.Sprintf("p%d:posts(first:50){items{id}} ", i)
		}
		_, r = e.contentRequest(t, key.Key, query+"}")
		if !r.hasErrorContaining("complexity") {
			t.Fatal("query complexity unbounded")
		}
	})
	t.Run("DataLayerCannotBypassPublicationPolicy", func(t *testing.T) {
		ctx := auth.WithContentReader(context.Background())
		if n, err := e.client.Post.Query().Count(ctx); err != nil || n != 2 {
			t.Fatalf("count=%d err=%v", n, err)
		}
		if _, err := e.client.Post.Get(ctx, e.posts["contributor"].ID); !ent.IsNotFound(err) {
			t.Fatalf("private Get: %v", err)
		}
		if _, err := e.client.User.Query().All(ctx); err == nil {
			t.Fatal("User read accepted")
		}
		if _, err := e.client.ApiKey.Query().All(ctx); err == nil {
			t.Fatal("ApiKey read accepted")
		}
		if _, err := e.client.Post.UpdateOneID(e.posts["editor"].ID).SetTitle("no").Save(ctx); err == nil {
			t.Fatal("write accepted")
		}
		if _, err := e.client.Post.Create().SetTitle("no").Save(auth.WithSystem(ctx)); err == nil {
			t.Fatal("system accidentally elevated content context")
		}
		if n, err := e.client.Photo.Query().Count(ctx); err != nil || n != 2 {
			t.Fatalf("photos count=%d err=%v", n, err)
		}
		if _, err := e.client.Photo.Get(ctx, privatePhoto.ID); !ent.IsNotFound(err) {
			t.Fatal("private photo accessible")
		}
		if n, err := e.client.Author.Query().Count(ctx); err != nil || n != 1 {
			t.Fatalf("author count=%d err=%v", n, err)
		}
	})
	t.Run("WithdrawalIsImmediate", func(t *testing.T) {
		e.client.Post.UpdateOneID(e.posts["editor"].ID).SetState(post.StateArchived).SaveX(sys)
		_, r := e.contentRequest(t, key.Key, fmt.Sprintf(`{post(id:"%d"){id}}`, e.posts["editor"].ID))
		r.mustNoErrors(t)
		if string(r.Data["post"]) != "null" {
			t.Fatal("withdrawn article still visible")
		}
		e.client.Post.UpdateOneID(e.posts["editor"].ID).SetState(post.StatePublished).SaveX(sys)
	})
	t.Run("GETTransport", func(t *testing.T) {
		req, _ := http.NewRequest("GET", e.ts.URL+`/graphql/content?query=%7Bposts%7BtotalCount%7D%7D`, nil)
		req.Header.Set("Authorization", "Bearer "+key.Key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"totalCount":2`)) {
			t.Fatalf("GET: %s", body)
		}
	})
	t.Run("RevocationAndIssuerDemotion", func(t *testing.T) {
		e.client.User.UpdateOne(e.users["admin"]).SetRole(user.RoleEditor).SaveX(sys)
		status, _ := e.contentRequest(t, key.Key, `{posts{totalCount}}`)
		if status != 401 {
			t.Fatal("demoted issuer's key remains authorized")
		}
		e.client.User.UpdateOne(e.users["admin"]).SetRole(user.RoleAdmin).SaveX(sys)
		q := fmt.Sprintf(`mutation{revokeContentApiKey(id:"%s")}`, key.ID)
		if !e.gql(t, "editor", q, nil).hasErrorContaining("access denied") {
			t.Fatal("editor revoked key")
		}
		r := e.gql(t, "moderator", q, nil)
		r.mustNoErrors(t)
		if string(r.Data["revokeContentApiKey"]) != "true" {
			t.Fatal("key not revoked")
		}
		status, _ = e.contentRequest(t, key.Key, `{posts{totalCount}}`)
		if status != 401 {
			t.Fatal("revoked key accepted")
		}
		r = e.gql(t, "admin", q, nil)
		r.mustNoErrors(t)
		if string(r.Data["revokeContentApiKey"]) != "false" {
			t.Fatal("repeat revocation not idempotent")
		}
		stored := e.client.ApiKey.Query().Where(apikey.KeyHashEQ(auth.HashAPIKey(cmsKey.Key))).OnlyX(sys)
		r = e.gql(t, "admin", fmt.Sprintf(`mutation{revokeContentApiKey(id:"%d")}`, stored.ID), nil)
		r.mustNoErrors(t)
		if string(r.Data["revokeContentApiKey"]) != "false" {
			t.Fatal("revoked CMS key through content mutation")
		}
	})
}
