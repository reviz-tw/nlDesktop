package admin

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hcchien/nl/access"
	"github.com/hcchien/nl/meta"
)

func cell(row map[string]any, key string) string {
	v := row[key]
	if v == nil {
		return "—"
	}
	if m, ok := v.(map[string]any); ok {
		if name, ok := m["name"].(string); ok {
			return name
		}
		return "—"
	}
	if b, ok := v.(bool); ok {
		if b {
			return "精選"
		}
		return "一般"
	}
	if key == "updatedAt" {
		if t, err := time.Parse(time.RFC3339, fmt.Sprint(v)); err == nil {
			return t.Local().Format("2006-01-02 15:04")
		}
	}
	s := fmt.Sprint(v)
	if s == "" {
		return "—"
	}
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

var funcs = template.FuncMap{
	"cell": cell,
	"icon": func(name string) template.HTML { return icons[strings.ToLower(name)] },
	"initial": func(s string) string {
		r := []rune(s)
		if len(r) > 0 {
			return strings.ToUpper(string(r[0]))
		}
		return "?"
	},
	"pill": func(key string) bool { return key == "state" || key == "role" || key == "isFeatured" },
	"pillClass": func(row map[string]any, key string) string {
		if key == "isFeatured" {
			if row[key] == true {
				return "published"
			}
			return "archived"
		}
		return fmt.Sprint(row[key])
	},
	"titlecase": func(s string) string {
		if s == "" {
			return s
		}
		r := []rune(s)
		return strings.ToUpper(string(r[0])) + string(r[1:])
	},
	"multiline": func(s string) bool { return s == "bio" || s == "description" || s == "brief" },
	"fieldLabel": func(s string) string {
		switch s {
		case "brief":
			return "brief（前言）"
		case "content":
			return "content（內文）"
		case "isFeatured":
			return "設為精選標籤"
		}
		return s
	},
	"queryURL": func(values url.Values, key, value string) string {
		q := url.Values{}
		for k, v := range values {
			q[k] = v
		}
		q.Del("after")
		q.Del("before")
		q.Set(key, value)
		return "?" + q.Encode()
	},
}

var pageTmpl = template.Must(template.New("page").Funcs(funcs).Parse(`<!DOCTYPE html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>{{.Title}} — Nocturne CMS</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/admin/static/nocturne.css"><link rel="stylesheet" href="/admin/static/cms.css">
</head><body><a class="skip-link" href="#main">跳至主要內容</a><div class="layout">
<aside class="sidebar"><a class="brand" href="/admin">{{icon "moon"}}<span>Nocturne CMS</span></a>
<nav aria-label="內容管理">{{range .Nav}}<a class="nav-item" href="/admin/l/{{.Name}}" {{if eq $.Title .Name}}aria-current="page"{{end}}>{{icon .Name}}<span>{{.Name}}</span><span class="nav-count">{{index $.Counts .Name}}</span></a>{{end}}</nav>
<div class="account"><div class="identity"><span class="avatar">{{initial .User}}</span><span>{{.User}} <span class="muted">({{.Role}})</span></span></div><a class="logout" href="/admin/logout">登出</a></div></aside>
<main id="main" class="cms-scroll">{{.Body}}</main></div>
<script src="/admin/static/admin.js" defer></script></body></html>`))

var loginTmpl = template.Must(template.New("login").Funcs(funcs).Parse(`<!DOCTYPE html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>登入 — Nocturne CMS</title><meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/admin/static/nocturne.css"><link rel="stylesheet" href="/admin/static/cms.css"></head><body class="login-page">
<form class="login-card card elev-md" method="post" action="/admin/login"><div class="brand">{{icon "moon"}}<span>Nocturne CMS</span></div>
<h2>登入</h2><p class="muted">使用你的 CMS 帳號繼續。</p>
{{if .}}<div class="err" role="alert">{{.}}</div>{{end}}
<div class="field"><label for="email">Email</label><input class="input" id="email" type="email" name="email" placeholder="admin@example.com" autocomplete="username" required autofocus></div>
<div class="field"><label for="password">密碼</label><input class="input" id="password" type="password" name="password" placeholder="••••••••" autocomplete="current-password" required></div>
<button class="btn btn-primary btn-block" type="submit">登入</button></form></body></html>`))

func renderLogin(w http.ResponseWriter, errMsg string, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	loginTmpl.Execute(w, errMsg)
}
func renderPage(w http.ResponseWriter, title string, s *session, nav []meta.List, body template.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pageTmpl.Execute(w, map[string]any{"Title": title, "User": s.name, "Role": s.role, "Nav": nav, "Counts": s.counts, "Body": body})
}

var listBodyTmpl = template.Must(template.New("list").Funcs(funcs).Parse(`
<section class="list-page"><header class="page-heading"><div><h1>{{.List.Name}}</h1><p class="muted">共 {{.Total}} 筆 · {{if .Filtered}}符合 {{.Matched}} 筆{{else}}顯示全部{{end}}</p></div>{{if .CanCreate}}<a class="btn btn-primary" href="/admin/l/{{.List.Name}}/new"><span aria-hidden="true">＋</span> 新增</a>{{end}}</header>
{{if .Notice}}<div class="notice" role="status">{{.Notice}}</div>{{end}}
<form class="list-controls" method="get" action="/admin/l/{{.List.Name}}"><div class="search"><span aria-hidden="true">⌕</span><input class="input" type="search" name="q" placeholder="搜尋…" aria-label="搜尋 {{.List.Name}}" value="{{.Query}}"></div>
<input type="hidden" name="direction" value="{{.Direction}}"><button class="btn btn-secondary search-submit" type="submit">搜尋</button>
{{if .Filters}}<div class="seg" role="group" aria-label="篩選">{{range .Filters}}<label class="seg-opt"><input type="radio" name="filter" value="{{.Value}}" {{if eq $.Filter .Value}}checked{{end}}>{{.Label}}</label>{{end}}</div>{{end}}</form>
<form class="bulk-form" method="post" action="/admin/l/{{.List.Name}}/bulk">
<input type="hidden" name="q" value="{{.Query}}"><input type="hidden" name="filter" value="{{.Filter}}">
{{if .CanBulk}}<div class="bulk-bar" hidden><span>已選取 <strong data-selected-count>0</strong> 筆</span>{{if .CanState}}<button class="btn btn-secondary" name="action" value="published">發佈</button><button class="btn btn-secondary" name="action" value="draft">設為草稿</button>{{end}}{{if .CanFeatured}}<button class="btn btn-secondary" name="action" value="featured">標示精選</button><button class="btn btn-secondary" name="action" value="normal">取消精選</button>{{end}}{{if .CanDelete}}<button class="btn btn-secondary danger" name="action" value="delete">刪除</button>{{end}}<button class="clear-selection" type="button">取消選取</button></div>{{end}}
<div class="table-scroll"><table class="table"><thead><tr>{{if .CanBulk}}<th class="check-cell"><input type="checkbox" aria-label="選取本頁全部" data-select-all {{if not .Rows}}disabled{{end}}></th>{{end}}<th class="id-cell">id</th><th>{{.List.LabelField}}</th>{{range .Extra}}<th {{if eq . "updatedAt"}}aria-sort="{{if eq $.Direction "ASC"}}ascending{{else}}descending{{end}}"{{end}}>{{if eq . "updatedAt"}}<a class="sort-link" href="{{queryURL $.Params "direction" $.NextDirection}}">updatedAt {{if eq $.Direction "ASC"}}↑{{else}}↓{{end}}</a>{{else}}{{if eq . "isFeatured"}}featured{{else}}{{.}}{{end}}{{end}}</th>{{end}}</tr></thead>
<tbody>{{$l:=.List}}{{$extra:=.Extra}}{{range $row:=.Rows}}<tr>{{if $.CanBulk}}<td><input type="checkbox" name="ids" value="{{cell $row "id"}}" aria-label="選取 {{cell $row $l.LabelField}}"></td>{{end}}<td class="muted">{{cell $row "id"}}</td><td class="primary-cell"><a href="/admin/l/{{$l.Name}}/{{cell $row "id"}}">{{cell $row $l.LabelField}}</a></td>{{range $extra}}<td>{{if pill .}}<span class="status-pill {{pillClass $row .}}">{{titlecase (cell $row .)}}</span>{{else}}<span class="muted">{{cell $row .}}</span>{{end}}</td>{{end}}</tr>{{end}}</tbody></table></div></form>
{{if not .Rows}}<div class="empty-state">{{icon .List.Name}}<p>沒有符合條件的資料</p>{{if .Filtered}}<a href="/admin/l/{{.List.Name}}">清除篩選</a>{{end}}</div>{{end}}
{{if or .Prev .Next}}<div class="pagination"><span class="muted">本頁 {{len .Rows}} 筆 · 每頁 20 筆</span>{{if .Prev}}<a class="btn btn-secondary btn-icon" aria-label="上一頁" href="{{.Prev}}">‹</a>{{else}}<button class="btn btn-secondary btn-icon" disabled aria-label="上一頁">‹</button>{{end}}{{if .Next}}<a class="btn btn-secondary btn-icon" aria-label="下一頁" href="{{.Next}}">›</a>{{else}}<button class="btn btn-secondary btn-icon" disabled aria-label="下一頁">›</button>{{end}}</div>{{end}}</section>`))

func extraColumns(l *meta.List) []string {
	switch l.Name {
	case "Post":
		return []string{"section", "state", "updatedAt"}
	case "Author":
		return []string{"bio", "posts", "updatedAt"}
	case "Category":
		return []string{"slug", "section", "updatedAt"}
	case "Photo":
		return []string{"url", "description", "updatedAt"}
	case "Section":
		return []string{"slug", "categories", "updatedAt"}
	case "Tag":
		return []string{"isFeatured", "sortOrder", "updatedAt"}
	case "User":
		return []string{"email", "role", "updatedAt"}
	}
	return []string{"updatedAt"}
}
func listBody(data map[string]any) template.HTML {
	var b strings.Builder
	listBodyTmpl.Execute(&b, data)
	return template.HTML(b.String())
}

var formBodyTmpl = template.Must(template.New("form").Funcs(funcs).Parse(`
<section class="detail-page"><a class="back-link" href="/admin/l/{{.List.Name}}">← {{.List.Name}}</a><header class="page-heading"><h1>{{if .ID}}{{.List.Name}} #{{.ID}}{{else}}新增 {{.List.Name}}{{end}}</h1>{{if and .ID .CanDelete}}<form method="post" action="/admin/l/{{.List.Name}}/{{.ID}}/delete" onsubmit="return confirm('確定刪除此筆資料？此操作無法復原。')"><button class="btn btn-secondary danger" type="submit">{{icon "trash"}}刪除</button></form>{{end}}</header>
{{if .Err}}<div class="err" role="alert">{{.Err}}</div>{{end}}
<form id="item-form" class="item" method="post"><div class="fields">
{{range .Fields}}<div class="field {{if eq .Type "richText"}}rich-field{{end}}" data-field="{{.Name}}"><div class="field-heading"><label id="label-{{.Name}}" for="field-{{.Name}}">{{fieldLabel .Name}}{{if .Required}} <span class="required">*</span>{{end}}</label>{{if eq .Type "richText"}}<button type="button" class="expand-editor" data-expand="rt-{{.Name}}">⛶ 展開全螢幕</button>{{end}}</div>
{{if .Note}}<p class="note">{{.Note}}</p>{{end}}
{{if eq .Type "boolean"}}<input id="field-{{.Name}}" type="checkbox" name="{{.Name}}" {{if .Checked}}checked{{end}} {{if .Readonly}}disabled{{end}}>
{{else if eq .Type "integer"}}<input class="input" id="field-{{.Name}}" type="number" name="{{.Name}}" value="{{.Value}}" {{if .Readonly}}readonly{{end}}>
{{else if eq .Type "timestamp"}}<input class="input" id="field-{{.Name}}" type="datetime-local" name="{{.Name}}" value="{{.Value}}" {{if .Readonly}}readonly{{end}}>
{{else if eq .Type "select"}}<div class="select-row"><select class="input" id="field-{{.Name}}" name="{{.Name}}" {{if .Readonly}}disabled{{end}}>{{$v:=.Value}}{{if not .Required}}<option value="">未指定</option>{{end}}{{range .Enum}}<option value="{{.}}" {{if eq . $v}}selected{{end}}>{{titlecase .}}</option>{{end}}</select>{{if .Value}}<span class="status-pill {{.Value}}" data-state-badge>{{titlecase .Value}}</span>{{end}}</div>
{{else if eq .Type "richText"}}<textarea id="rt-{{.Name}}" name="{{.Name}}" hidden>{{.JSON}}</textarea><div class="rt" data-input="rt-{{.Name}}" data-label="{{fieldLabel .Name}}" data-readonly="{{.Readonly}}"></div>
{{else if eq .Type "password"}}<input class="input" id="field-{{.Name}}" type="password" name="{{.Name}}" autocomplete="new-password" {{if .Required}}required{{end}} placeholder="{{if $.ID}}留空表示不變更{{end}}">
{{else if eq .Type "relationship"}}{{if or .Denied .Readonly}}<div class="note">此欄位唯讀{{if .Denied}}（無權瀏覽 {{.Ref}}）{{end}}</div>{{else}}<input type="hidden" name="_present_{{.Name}}" value="1"><div class="relpicker" data-name="{{.Name}}" data-list="{{.Ref}}" data-many="{{.Many}}" data-selected="{{.SelectedJSON}}"><div class="chips"></div><input id="field-{{.Name}}" class="input relsearch" type="text" aria-label="搜尋 {{.Name}}" placeholder="搜尋 {{.Ref}}…" autocomplete="off"></div>{{end}}
{{else}}{{if multiline .Name}}<textarea class="input" id="field-{{.Name}}" name="{{.Name}}" rows="3" {{if .Readonly}}readonly{{end}}>{{.Value}}</textarea>{{else}}<input class="input" id="field-{{.Name}}" type="{{if eq .Name "email"}}email{{else}}text{{end}}" name="{{.Name}}" value="{{.Value}}" {{if .Required}}required{{end}} {{if .Readonly}}readonly{{end}}>{{end}}{{end}}</div>{{end}}
</div><footer class="save-bar"><span data-save-status role="status">{{if .Err}}儲存失敗，請檢查欄位{{else if .ID}}已儲存{{else}}尚未建立{{end}}</span><a class="btn btn-secondary" href="/admin/l/{{.List.Name}}">取消</a>{{if .CanSave}}<button class="btn btn-primary" type="submit">{{if .ID}}儲存變更{{else}}建立{{end}}</button>{{end}}</footer></form></section>`))

func formBody(l *meta.List, id string, fields []formField, errMsg string, s *session) template.HTML {
	var b strings.Builder
	op := access.OpCreate
	if id != "" {
		op = access.OpUpdate
	}
	formBodyTmpl.Execute(&b, map[string]any{"List": l, "ID": id, "Fields": fields, "Err": errMsg, "CanSave": access.CanOperate(l.Name, op, s.role), "CanDelete": access.CanOperate(l.Name, access.OpDelete, s.role)})
	return template.HTML(b.String())
}

var errorBodyTmpl = template.Must(template.New("err").Parse(`<section class="detail-page"><h1>無法完成操作</h1>{{range .}}<div class="err" role="alert">{{.}}</div>{{end}}<a class="btn btn-secondary" href="/admin">返回 CMS</a></section>`))

func errorBody(errs []string) template.HTML {
	var b strings.Builder
	errorBodyTmpl.Execute(&b, errs)
	return template.HTML(b.String())
}
