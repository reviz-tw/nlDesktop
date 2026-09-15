// Package admin 提供 server-rendered、schema-driven 的管理介面（/admin）。
//
// 所有資料操作經 in-process GraphQL 呼叫、帶登入者自己的 token，
// 因此權限與 GraphQL API / MCP 完全一致（同一個資料層關卡），
// admin 本身沒有任何特權路徑。表單由 meta（nl DSL registry）動態生成。
package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hcchien/nl/access"
	"github.com/hcchien/nl/meta"
)

// 前端 bundle（Tiptap 編輯器 + 關聯 picker），由 converter 以同一份
// Tiptap schema 建置：cd converter && npm run build
//
//go:embed static
var staticFS embed.FS

const cookieName = "nl_admin"

// Handler 是 admin UI 的 HTTP handlers。
type Handler struct {
	gql    http.Handler // CMS 的 /graphql handler（in-process 呼叫）
	secret []byte
}

// New 建立掛在 /admin 底下的 handler。
func New(gql http.Handler, secret []byte) http.Handler {
	h := &Handler{gql: gql, secret: secret}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", h.loginForm)
	mux.HandleFunc("POST /admin/login", h.loginSubmit)
	mux.HandleFunc("GET /admin/logout", h.logout)
	mux.HandleFunc("GET /admin", h.requireAuth(h.home))
	mux.HandleFunc("POST /admin/l/{list}/bulk", h.requireAuth(h.bulkSubmit))
	mux.HandleFunc("GET /admin/l/{list}", h.requireAuth(h.listView))
	mux.HandleFunc("GET /admin/l/{list}/new", h.requireAuth(h.itemForm))
	mux.HandleFunc("POST /admin/l/{list}/new", h.requireAuth(h.itemSubmit))
	mux.HandleFunc("GET /admin/l/{list}/{id}", h.requireAuth(h.itemForm))
	mux.HandleFunc("POST /admin/l/{list}/{id}", h.requireAuth(h.itemSubmit))
	mux.HandleFunc("POST /admin/l/{list}/{id}/delete", h.requireAuth(h.itemDelete))
	mux.HandleFunc("GET /admin/api/options", h.requireAuth(h.optionsAPI))
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", http.FileServerFS(static)))
	return mux
}

// optionsAPI 供關聯 picker 搜尋目標 list（label contains，不分大小寫）。
// 經 GraphQL 查詢，權限與其他操作一致。
func (h *Handler) optionsAPI(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.URL.Query().Get("list"))
	if !ok {
		http.Error(w, "unknown list", http.StatusBadRequest)
		return
	}
	q := fmt.Sprintf(`query($where: %sWhereInput){ items: %s(first: 20, where: $where){ edges{node{ id %s }} } }`,
		l.Name, l.QueryField, l.LabelField)
	where := map[string]any{l.LabelField + "ContainsFold": r.URL.Query().Get("q")}
	data, errs := h.exec(s.token, q, map[string]any{"where": where})
	type opt struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	out := []opt{}
	if len(errs) == 0 && data["items"] != nil {
		var conn struct {
			Edges []struct {
				Node map[string]any `json:"node"`
			} `json:"edges"`
		}
		if json.Unmarshal(data["items"], &conn) == nil {
			for _, e := range conn.Edges {
				id, _ := e.Node["id"].(string)
				label, _ := e.Node[l.LabelField].(string)
				out = append(out, opt{ID: id, Label: label})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ---- auth ----

type session struct {
	token  string
	name   string
	role   string
	counts map[string]string
}

func (h *Handler) sessionFrom(r *http.Request) *session {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	// 以 me query 驗證 token 並取得身分（無效 token 會拿到 null）
	data, errs := h.exec(c.Value, `{me{name role}}`, nil)
	if len(errs) > 0 || data["me"] == nil {
		return nil
	}
	var me struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data["me"], &me); err != nil || me.Role == "" {
		return nil
	}
	return &session{token: c.Value, name: me.Name, role: me.Role}
}

func (h *Handler) requireAuth(next func(http.ResponseWriter, *http.Request, *session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := h.sessionFrom(r)
		if s == nil {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r, s)
	}
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	renderLogin(w, "", http.StatusOK)
}

func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	data, errs := h.exec("", `mutation($e: String!, $p: String!){login(email:$e,password:$p){token}}`,
		map[string]any{"e": r.PostFormValue("email"), "p": r.PostFormValue("password")})
	if len(errs) > 0 || data["login"] == nil {
		renderLogin(w, "帳號或密碼錯誤", http.StatusUnauthorized)
		return
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data["login"], &payload); err != nil {
		renderLogin(w, "登入失敗", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: payload.Token, Path: "/admin",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int((7 * 24 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/admin", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// ---- in-process GraphQL ----

func (h *Handler) exec(token, query string, vars map[string]any) (map[string]json.RawMessage, []string) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.gql.ServeHTTP(rec, req)
	var out struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return nil, []string{"internal error: bad GraphQL response"}
	}
	msgs := make([]string, len(out.Errors))
	for i, e := range out.Errors {
		msgs[i] = e.Message
	}
	return out.Data, msgs
}

// ---- pages ----

// navLists 回傳該角色可查詢的 lists（首頁與側欄）。
func navLists(role string) []meta.List {
	var out []meta.List
	for _, l := range meta.All() {
		if access.CanOperate(l.Name, access.OpQuery, role) {
			out = append(out, l)
		}
	}
	return out
}

// Navigation counts follow the same viewer and row-level restrictions as lists.
func (h *Handler) renderPage(w http.ResponseWriter, title string, s *session, nav []meta.List, body template.HTML) {
	s.counts = map[string]string{}
	for _, l := range nav {
		data, errs := h.exec(s.token, fmt.Sprintf(`{items:%s(first:0){totalCount}}`, l.QueryField), nil)
		var count struct{ TotalCount int }
		if len(errs) == 0 && json.Unmarshal(data["items"], &count) == nil {
			s.counts[l.Name] = strconv.Itoa(count.TotalCount)
		}
	}
	renderPage(w, title, s, nav, body)
}
func (h *Handler) home(w http.ResponseWriter, r *http.Request, s *session) {
	r.SetPathValue("list", "Post")
	h.listView(w, r, s)
}

type filterOption struct{ Value, Label string }

func listFilters(l *meta.List) []filterOption {
	var opts []filterOption
	switch l.Name {
	case "Post", "User":
		field := "state"
		if l.Name == "User" {
			field = "role"
		}
		opts = append(opts, filterOption{"all", "全部"})
		for _, f := range l.Fields {
			if f.Name == field {
				for _, v := range f.Enum {
					opts = append(opts, filterOption{v, strings.ToUpper(v[:1]) + v[1:]})
				}
			}
		}
	case "Tag":
		opts = []filterOption{{"all", "全部"}, {"featured", "精選"}, {"normal", "一般"}}
	}
	return opts
}

// Query only readable fields; field restrictions still apply in GraphQL itself.
func selectionFor(l *meta.List, role string) string {
	copyList := *l
	copyList.Fields = nil
	for _, f := range l.Fields {
		if access.FieldReadAllowed(l.Name, f.Name, role) {
			copyList.Fields = append(copyList.Fields, f)
		}
	}
	return copyList.Selection()
}
func (h *Handler) listView(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	params := r.URL.Query()
	query := strings.TrimSpace(params.Get("q"))
	filter := params.Get("filter")
	if filter == "" {
		filter = "all"
	}
	opts := listFilters(l)
	valid := filter == "all"
	for _, o := range opts {
		if o.Value == filter {
			valid = true
		}
	}
	if !valid {
		filter = "all"
	}
	where := map[string]any{}
	if query != "" {
		where[l.LabelField+"ContainsFold"] = query
	}
	if filter != "all" {
		switch l.Name {
		case "Post":
			where["state"] = filter
		case "User":
			where["role"] = filter
		case "Tag":
			where["isFeatured"] = filter == "featured"
		}
	}
	direction := params.Get("direction")
	if direction != "ASC" {
		direction = "DESC"
	}
	args := "first:20"
	cursor := params.Get("after")
	if before := params.Get("before"); before != "" {
		args = "last:20, before:$cursor"
		cursor = before
	} else if cursor != "" {
		args += ", after:$cursor"
	}
	defs := fmt.Sprintf("$where:%sWhereInput", l.Name)
	vars := map[string]any{"where": where}
	if cursor != "" {
		defs += ", $cursor:Cursor"
		vars["cursor"] = cursor
	}
	q := fmt.Sprintf(`query(%s){items:%s(%s,where:$where,orderBy:{field:UPDATED_AT,direction:%s}){totalCount pageInfo{hasNextPage hasPreviousPage startCursor endCursor} edges{node{%s}}} total:%s(first:0){totalCount}}`, defs, l.QueryField, args, direction, selectionFor(l, s.role), l.QueryField)
	data, errs := h.exec(s.token, q, vars)
	if len(errs) > 0 {
		h.renderPage(w, l.Name, s, navLists(s.role), errorBody(errs))
		return
	}
	var conn struct {
		TotalCount int
		PageInfo   struct {
			HasNextPage, HasPreviousPage bool
			StartCursor, EndCursor       string
		}
		Edges []struct{ Node map[string]any }
	}
	if err := json.Unmarshal(data["items"], &conn); err != nil {
		h.renderPage(w, l.Name, s, navLists(s.role), errorBody([]string{err.Error()}))
		return
	}
	var total struct{ TotalCount int }
	json.Unmarshal(data["total"], &total)
	rows := make([]map[string]any, 0, len(conn.Edges))
	for _, e := range conn.Edges {
		rows = append(rows, e.Node)
	}
	// Display related counts using the target list's own permission-filtered query.
	target, relation, column := "", "", ""
	if l.Name == "Author" {
		target, relation, column = "Post", "hasWritersWith", "posts"
	}
	if l.Name == "Section" {
		target, relation, column = "Category", "hasSectionWith", "categories"
	}
	if target != "" && access.CanOperate(target, access.OpQuery, s.role) && len(rows) > 0 {
		targetList, _ := meta.Get(target)
		var countQuery strings.Builder
		countQuery.WriteString("{")
		for i, row := range rows {
			id, err := strconv.Atoi(fmt.Sprint(row["id"]))
			if err == nil {
				fmt.Fprintf(&countQuery, "r%d:%s(first:0,where:{%s:[{id:%d}]}){totalCount} ", i, targetList.QueryField, relation, id)
			}
		}
		countQuery.WriteString("}")
		counts, errs := h.exec(s.token, countQuery.String(), nil)
		if len(errs) == 0 {
			for i, row := range rows {
				var result struct{ TotalCount int }
				if json.Unmarshal(counts[fmt.Sprintf("r%d", i)], &result) == nil {
					row[column] = result.TotalCount
				}
			}
		}
	}
	pageURL := func(key, value string) string {
		v := url.Values{"q": {query}, "filter": {filter}, "direction": {direction}, key: {value}}
		return "/admin/l/" + l.Name + "?" + v.Encode()
	}
	prev, next := "", ""
	if conn.PageInfo.HasPreviousPage {
		prev = pageURL("before", conn.PageInfo.StartCursor)
	}
	if conn.PageInfo.HasNextPage {
		next = pageURL("after", conn.PageInfo.EndCursor)
	}
	canDelete := access.CanOperate(l.Name, access.OpDelete, s.role)
	canUpdate := access.CanOperate(l.Name, access.OpUpdate, s.role)
	canState := l.Name == "Post" && canUpdate && access.FieldWriteAllowed(l.Name, "state", access.OpUpdate, s.role)
	canFeatured := l.Name == "Tag" && canUpdate
	nextDirection := "ASC"
	if direction == "ASC" {
		nextDirection = "DESC"
	}
	h.renderPage(w, l.Name, s, navLists(s.role), listBody(map[string]any{"List": l, "Rows": rows, "Total": total.TotalCount, "Matched": conn.TotalCount, "Extra": extraColumns(l), "Next": next, "Prev": prev, "Query": query, "Filter": filter, "Filtered": query != "" || filter != "all", "Filters": opts, "Direction": direction, "NextDirection": nextDirection, "Params": params, "CanCreate": access.CanOperate(l.Name, access.OpCreate, s.role), "CanDelete": canDelete, "CanState": canState, "CanFeatured": canFeatured, "CanBulk": canDelete || canState || canFeatured, "Notice": params.Get("notice")}))
}

func (h *Handler) bulkSubmit(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	action := r.PostFormValue("action")
	op := access.OpUpdate
	if action == "delete" {
		op = access.OpDelete
	}
	allowed := access.CanOperate(l.Name, op, s.role)
	switch action {
	case "delete":
	case "published", "draft":
		allowed = allowed && l.Name == "Post" && access.FieldWriteAllowed(l.Name, "state", op, s.role)
	case "featured", "normal":
		allowed = allowed && l.Name == "Tag"
	default:
		allowed = false
	}
	if !allowed {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	ids := r.PostForm["ids"]
	if len(ids) == 0 || len(ids) > 20 {
		http.Error(w, "請選取 1–20 筆資料", http.StatusBadRequest)
		return
	}
	seen := map[int]bool{}
	parsed := []int{}
	for _, raw := range ids {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		if !seen[id] {
			seen[id] = true
			parsed = append(parsed, id)
		}
	}
	succeeded := 0
	var failures []string
	for _, id := range parsed {
		var errs []string
		if action == "delete" {
			_, errs = h.exec(s.token, fmt.Sprintf(`mutation($id:ID!){delete%s(id:$id)}`, l.Name), map[string]any{"id": id})
		} else {
			input := map[string]any{}
			switch action {
			case "published", "draft":
				input["state"] = action
				if action == "published" {
					data, readErrs := h.exec(s.token, `query($id:ID){posts(first:1,where:{id:$id}){edges{node{publishTime}}}}`, map[string]any{"id": id})
					var posts struct {
						Edges []struct{ Node struct{ PublishTime *string } }
					}
					if len(readErrs) > 0 || json.Unmarshal(data["posts"], &posts) != nil || len(posts.Edges) == 0 {
						failures = append(failures, fmt.Sprintf("#%d 無法讀取", id))
						continue
					}
					if posts.Edges[0].Node.PublishTime == nil {
						input["publishTime"] = time.Now().Format(time.RFC3339)
					}
				}
			case "featured", "normal":
				input["isFeatured"] = action == "featured"
			}
			_, errs = h.exec(s.token, fmt.Sprintf(`mutation($id:ID!,$input:Update%sInput!){update%s(id:$id,input:$input){id}}`, l.Name, l.Name), map[string]any{"id": id, "input": input})
		}
		if len(errs) > 0 {
			failures = append(failures, fmt.Sprintf("#%d：%s", id, strings.Join(errs, "；")))
		} else {
			succeeded++
		}
	}
	notice := fmt.Sprintf("已完成 %d 筆操作", succeeded)
	if len(failures) > 0 {
		notice += "；" + strings.Join(failures, "；")
	}
	v := url.Values{"notice": {notice}, "q": {r.PostFormValue("q")}, "filter": {r.PostFormValue("filter")}}
	http.Redirect(w, r, "/admin/l/"+l.Name+"?"+v.Encode(), http.StatusSeeOther)
}

func (h *Handler) itemForm(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	var item map[string]any
	if id != "" {
		// 以 where:{id} 查單筆（node(id) 需要 global-unique-ID，本框架採 per-table id）
		q := fmt.Sprintf(`query($id: ID){ items: %s(where:{id: $id}, first: 1){ edges{node{%s}} } }`,
			l.QueryField, selectionFor(l, s.role))
		idNum, _ := strconv.Atoi(id)
		data, errs := h.exec(s.token, q, map[string]any{"id": idNum})
		if len(errs) > 0 {
			h.renderPage(w, l.Name, s, navLists(s.role), errorBody(errs))
			return
		}
		var conn struct {
			Edges []struct {
				Node map[string]any `json:"node"`
			} `json:"edges"`
		}
		if err := json.Unmarshal(data["items"], &conn); err != nil || len(conn.Edges) == 0 {
			h.renderPage(w, l.Name, s, navLists(s.role), errorBody([]string{"找不到項目（或無權限）"}))
			return
		}
		item = conn.Edges[0].Node
	}
	fields := h.buildFormFields(s, l, item)
	h.renderPage(w, l.Name, s, navLists(s.role), formBody(l, id, fields, "", s))
}

func (h *Handler) itemSubmit(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	input, err := h.formToInput(l, r, id != "")
	if err == nil {
		var errs []string
		if id == "" {
			q := fmt.Sprintf(`mutation($input: Create%sInput!){ item: create%s(input: $input){ id } }`, l.Name, l.Name)
			var data map[string]json.RawMessage
			data, errs = h.exec(s.token, q, map[string]any{"input": input})
			if len(errs) == 0 {
				var created struct {
					ID string `json:"id"`
				}
				json.Unmarshal(data["item"], &created)
				http.Redirect(w, r, fmt.Sprintf("/admin/l/%s/%s", l.Name, created.ID), http.StatusFound)
				return
			}
		} else {
			idNum, _ := strconv.Atoi(id)
			q := fmt.Sprintf(`mutation($id: ID!, $input: Update%sInput!){ item: update%s(id: $id, input: $input){ id } }`, l.Name, l.Name)
			_, errs = h.exec(s.token, q, map[string]any{"id": idNum, "input": input})
			if len(errs) == 0 {
				http.Redirect(w, r, fmt.Sprintf("/admin/l/%s/%s", l.Name, id), http.StatusFound)
				return
			}
		}
		err = fmt.Errorf("%s", strings.Join(errs, "；"))
	}
	// 失敗：重建表單並顯示錯誤
	var item map[string]any
	if id != "" {
		item = map[string]any{}
		data, errs := h.exec(s.token, fmt.Sprintf(`query($id:ID){items:%s(first:1,where:{id:$id}){edges{node{%s}}}}`, l.QueryField, selectionFor(l, s.role)), map[string]any{"id": id})
		var current struct {
			Edges []struct{ Node map[string]any }
		}
		if len(errs) == 0 && json.Unmarshal(data["items"], &current) == nil && len(current.Edges) > 0 {
			item = current.Edges[0].Node
		}
	}
	fields := h.buildFormFields(s, l, item)
	h.restoreSubmittedFields(s, fields, r)
	h.renderPage(w, l.Name, s, navLists(s.role), formBody(l, id, fields, err.Error(), s))
}

func (h *Handler) itemDelete(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	idNum, _ := strconv.Atoi(r.PathValue("id"))
	q := fmt.Sprintf(`mutation($id: ID!){ delete%s(id: $id) }`, l.Name)
	if _, errs := h.exec(s.token, q, map[string]any{"id": idNum}); len(errs) > 0 {
		h.renderPage(w, l.Name, s, navLists(s.role), errorBody(errs))
		return
	}
	http.Redirect(w, r, "/admin/l/"+l.Name, http.StatusFound)
}

// ---- 表單建構 ----

type option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type formField struct {
	Name         string
	Type         string
	Required     bool
	Note         string
	Value        string   // text / integer / timestamp
	Checked      bool     // boolean
	Enum         []string // select
	JSON         string   // richText（PM doc JSON，編輯器初始內容）
	Ref          string   // relationship
	Many         bool
	SelectedJSON string // relationship 現值 [{id,label}]，picker 初始 chips
	Readonly     bool
	Denied       bool // 無權查詢關聯目標 list（欄位唯讀）
}

// buildFormFields 依 meta 欄位型別產生表單欄位（含關聯選項與現值）。
func (h *Handler) buildFormFields(s *session, l *meta.List, item map[string]any) []formField {
	var out []formField
	for _, f := range l.Fields {
		if f.ReadOnly || f.Name == "createdBy" || f.Name == "createdAt" || f.Name == "updatedAt" {
			continue
		}
		ff := formField{Name: f.Name, Type: f.Type, Required: f.Required, Note: f.Note, Enum: f.Enum, Ref: f.Ref, Many: f.Many}
		op := access.OpCreate
		if item != nil {
			op = access.OpUpdate
		}
		ff.Readonly = !access.CanOperate(l.Name, op, s.role) || !access.FieldWriteAllowed(l.Name, f.Name, op, s.role)
		// Editorial hints omit storage/API implementation details from the UI.
		if f.Type == "richText" {
			ff.Note = ""
		}
		v := item[f.Name]
		switch f.Type {
		case "boolean":
			ff.Checked, _ = v.(bool)
		case "richText":
			if v != nil {
				b, _ := json.MarshalIndent(v, "", "  ")
				ff.JSON = string(b)
			}
		case "relationship":
			ff.Denied = !access.CanOperate(f.Ref, access.OpQuery, s.role)
			ff.SelectedJSON = selectedJSON(f, v)
		case "timestamp":
			if str, ok := v.(string); ok && str != "" {
				if t, err := time.Parse(time.RFC3339, str); err == nil {
					ff.Value = t.Local().Format("2006-01-02T15:04")
				}
			}
		case "select":
			ff.Value, _ = v.(string)
			if item == nil && f.Name == "state" {
				ff.Value = "draft"
			}
			if item == nil && f.Name == "role" {
				ff.Value = "contributor"
			}
		default:
			switch tv := v.(type) {
			case string:
				ff.Value = tv
			case float64:
				ff.Value = strconv.FormatFloat(tv, 'f', -1, 64)
			}
		}
		out = append(out, ff)
	}
	if l.Name == "User" && access.CanOperate(l.Name, access.OpUpdate, s.role) {
		out = append(out, formField{Name: "password", Type: "password", Required: item == nil})
	}
	order := []string{"state", "role", "section", "title", "name", "email", "subtitle", "slug", "url", "publishTime", "otherByline", "bio", "description", "brief", "content", "categories", "tags", "writers", "heroImage", "image", "relatedPosts", "sortOrder", "isFeatured", "password"}
	ranks := map[string]int{}
	for i, k := range order {
		ranks[k] = i + 1
	}
	sort.SliceStable(out, func(i, j int) bool { return ranks[out[i].Name] < ranks[out[j].Name] })
	return out
}

// Keep submitted content on validation failure, including rich text and relationships.
func (h *Handler) restoreSubmittedFields(s *session, fields []formField, r *http.Request) {
	for i := range fields {
		f := &fields[i]
		if f.Readonly || f.Denied {
			continue
		}
		f.Value = r.PostFormValue(f.Name)
		f.Checked = f.Value == "on"
		if f.Type == "password" {
			f.Value = ""
			continue
		}
		if f.Type == "richText" {
			f.JSON = r.PostFormValue(f.Name)
		}
		if f.Type == "relationship" && !f.Denied {
			opts := []option{}
			target, ok := meta.Get(f.Ref)
			if !ok {
				continue
			}
			for _, id := range r.PostForm[f.Name] {
				data, errs := h.exec(s.token, fmt.Sprintf(`query($id:ID){items:%s(first:1,where:{id:$id}){edges{node{id %s}}}}`, target.QueryField, target.LabelField), map[string]any{"id": id})
				var c struct {
					Edges []struct{ Node map[string]any }
				}
				if len(errs) == 0 && json.Unmarshal(data["items"], &c) == nil && len(c.Edges) > 0 {
					opts = append(opts, option{ID: id, Label: cell(c.Edges[0].Node, target.LabelField)})
				}
			}
			b, _ := json.Marshal(opts)
			f.SelectedJSON = string(b)
		}
	}
}

// selectedJSON 將關聯現值序列化為 [{id,label}]（picker 初始 chips）。
func selectedJSON(f meta.Field, current any) string {
	label := "name"
	if target, ok := meta.Get(f.Ref); ok {
		label = target.LabelField
	}
	var out []option
	add := func(m map[string]any) {
		id, _ := m["id"].(string)
		lb, _ := m[label].(string)
		if id != "" {
			out = append(out, option{ID: id, Label: lb})
		}
	}
	switch cv := current.(type) {
	case map[string]any: // 單值關聯 {id label}
		add(cv)
	case []any: // 多值關聯
		for _, item := range cv {
			if m, ok := item.(map[string]any); ok {
				add(m)
			}
		}
	}
	if out == nil {
		return "[]"
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// formToInput 將表單值轉為 Create/Update input 物件。
func (h *Handler) formToInput(l *meta.List, r *http.Request, isUpdate bool) (map[string]any, error) {
	input := map[string]any{}
	if l.Name == "User" {
		if password := r.PostFormValue("password"); password != "" {
			input["password"] = password
		}
	}
	for _, f := range l.Fields {
		if f.ReadOnly || f.Name == "createdBy" || f.Name == "createdAt" || f.Name == "updatedAt" {
			continue
		}
		// Omitted controls differ from intentionally cleared values.
		if _, present := r.PostForm[f.Name]; !present && isUpdate && f.Type != "boolean" && f.Type != "relationship" {
			continue
		}
		raw := strings.TrimSpace(r.PostFormValue(f.Name))
		switch f.Type {
		case "boolean":
			input[f.Name] = r.PostFormValue(f.Name) == "on"
		case "integer":
			if raw == "" {
				continue
			}
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("%s 必須是整數", f.Name)
			}
			input[f.Name] = n
		case "timestamp":
			if raw == "" {
				if isUpdate && !f.Required {
					input["clear"+upperFirst(f.Name)] = true
				}
				continue
			}
			t, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local)
			if err != nil {
				return nil, fmt.Errorf("%s 時間格式錯誤", f.Name)
			}
			input[f.Name] = t.Format(time.RFC3339)
		case "select":
			if raw != "" {
				input[f.Name] = raw
			}
		case "richText":
			if raw == "" {
				if isUpdate {
					input["clear"+upperFirst(f.Name)] = true
				}
				continue
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				return nil, fmt.Errorf("%s 必須是合法的 JSON（ProseMirror doc）", f.Name)
			}
			input[f.Name] = doc
		case "password":
			if raw != "" {
				input[f.Name] = raw
			} else if !isUpdate {
				return nil, fmt.Errorf("%s 必填", f.Name)
			}
		case "relationship":
			// 欄位未渲染（無權瀏覽目標 list）時不得動到關聯：
			// 多選清空與未渲染在表單上無法區分，靠 hidden marker 辨別
			if r.PostFormValue("_present_"+f.Name) != "1" {
				continue
			}
			ids := r.PostForm[f.Name]
			var idNums []int
			for _, s := range ids {
				if s == "" {
					continue
				}
				n, _ := strconv.Atoi(s)
				idNums = append(idNums, n)
			}
			singular := singularName(f.Name)
			if f.Many {
				if isUpdate {
					// set 語意：先清空再加入
					input["clear"+upperFirst(f.Name)] = true
					if len(idNums) > 0 {
						input["add"+upperFirst(singular)+"IDs"] = idNums
					}
				} else if len(idNums) > 0 {
					input[singular+"IDs"] = idNums
				}
			} else {
				if len(idNums) > 0 {
					input[f.Name+"ID"] = idNums[0]
				} else if isUpdate {
					input["clear"+upperFirst(f.Name)] = true
				}
			}
		default: // text
			if raw == "" && isUpdate && !f.Required {
				input["clear"+upperFirst(f.Name)] = true
				continue
			}
			if raw != "" || !isUpdate {
				input[f.Name] = raw
			}
		}
	}
	return input, nil
}

func singularName(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "s"):
		return s[:len(s)-1]
	default:
		return s
	}
}

func upperFirst(s string) string {
	if s == "url" {
		return "URL"
	}
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
