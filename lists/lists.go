// Package lists 是本 CMS 的 schema 宣告 —— 唯一的真相來源。
// 每個 list 一個宣告，`nl gen` 展開成 ent schema 與 GraphQL mutations/resolvers；
// access 權限規則與 MCP metadata 於執行期直接從這裡導出。
//
// 對照 e-info CMS（Keystone 6）的核心 lists。
package lists

import (
	"github.com/hcchien/nl/nl"
	"github.com/hcchien/nl/nl/field"
)

// 角色（對照 e-info 的 User.role）。
// 注意：lists 是 codegen 的輸入，不可 import 依賴 ent 的套件（access 等）。
const (
	RoleAdmin       = "admin"
	RoleModerator   = "moderator"
	RoleEditor      = "editor"
	RoleContributor = "contributor"
)

var (
	staff    = nl.Roles(RoleAdmin, RoleModerator, RoleEditor)
	managers = nl.Roles(RoleAdmin, RoleModerator)
	admins   = nl.Roles(RoleAdmin)
)

// 標準後台物件的權限：staff 可查、managers 可寫、admin 可刪。
var backOffice = nl.Ops{
	Query:  staff,
	Create: managers,
	Update: managers,
	Delete: admins,
}

var User = nl.List("User",
	nl.Fields{
		"name":     field.Text(field.Required()),
		"email":    field.Text(field.Required(), field.Unique()),
		"password": field.Password(field.Required()),
		"role": field.Select(
			[]string{RoleAdmin, RoleModerator, RoleEditor, RoleContributor},
			field.Required(),
		),
	},
	backOffice,
	nl.WithDescription("CMS 使用者。email 僅 admin/moderator 可讀（本人除外）。"),
	nl.WithFieldAccess(map[string]nl.FieldRule{
		"email": {Read: []string{RoleAdmin, RoleModerator}},
	}),
)

var Post = nl.List("Post",
	nl.Fields{
		"title":    field.Text(field.Required()),
		"subtitle": field.Text(),
		"state": field.Select(
			[]string{"draft", "scheduled", "published", "archived", "invisible"},
			field.Default("draft"),
			field.Note("非 draft 時 publishTime 必填"),
		),
		"publishTime":   field.Timestamp(field.Orderable("PUBLISH_TIME")),
		"brief":         field.RichText(field.Note("前言（ProseMirror doc JSON；MCP 可傳 HTML 字串自動轉換）")),
		"content":       field.RichText(field.Note("內文（ProseMirror doc JSON；MCP 可傳 HTML 字串自動轉換）")),
		"contentHtml":   field.Text(field.ReadOnly(), field.Note("由 content 自動產生的網站 HTML；視覺樣式由網站 CSS 控制")),
		"renderVersion": field.Integer(field.Default(0), field.ReadOnly(), field.Note("HTML renderer 版本；0 表示尚未產生")),
		"otherByline":   field.Text(),
		"section":       field.Relationship("Section", field.Inverse("posts")),
		"categories":    field.RelationshipMany("Category", field.Inverse("posts")),
		"tags":          field.RelationshipMany("Tag", field.Inverse("posts")),
		"writers":       field.RelationshipMany("Author", field.Inverse("posts")),
		"heroImage":     field.Relationship("Photo"),
		"relatedPosts": field.RelationshipMany("Post",
			field.Note("相關文章")),
	},
	nl.Ops{
		Query:  managers.OwnedBy(RoleEditor, RoleContributor),
		Create: nl.Roles(RoleAdmin, RoleModerator, RoleEditor, RoleContributor),
		Update: managers.OwnedBy(RoleEditor, RoleContributor),
		Delete: admins,
	},
	nl.WithDescription("文章。editor/contributor 僅能查詢與修改自己建立的文章；contributor 不可修改 state。"),
	nl.WithLabelField("title"),
	nl.TrackCreator(),
	nl.WithFieldAccess(map[string]nl.FieldRule{
		// contributor 不能修改狀態（不能發佈文章）
		"state": {Update: []string{RoleAdmin, RoleModerator, RoleEditor}},
	}),
)

var Author = nl.List("Author",
	nl.Fields{
		"name":  field.Text(field.Required()),
		"bio":   field.Text(),
		"image": field.Relationship("Photo"),
	},
	backOffice,
	nl.WithDescription("作者/記者（非登入使用者）。"),
)

var Section = nl.List("Section",
	nl.Fields{
		"slug":        field.Text(field.Required(), field.Unique(), field.Match(`^[a-zA-Z0-9]+$`), field.Note("限英文與數字")),
		"name":        field.Text(field.Required()),
		"description": field.Text(),
	},
	backOffice,
	nl.WithDescription("大分類。"),
)

var Category = nl.List("Category",
	nl.Fields{
		"slug":        field.Text(field.Required(), field.Unique(), field.Match(`^[a-zA-Z0-9]+$`), field.Note("限英文與數字")),
		"name":        field.Text(field.Required()),
		"description": field.Text(),
		"section":     field.Relationship("Section", field.Inverse("categories")),
	},
	backOffice,
	nl.WithDescription("中分類，隸屬於一個 Section。"),
)

var Tag = nl.List("Tag",
	nl.Fields{
		"name":       field.Text(field.Required(), field.Unique()),
		"brief":      field.Text(),
		"isFeatured": field.Boolean(field.Default(false)),
		"sortOrder":  field.Integer(field.Orderable("SORT_ORDER")),
	},
	nl.Ops{Query: staff, Create: staff, Update: staff, Delete: admins},
	nl.WithDescription("標籤。"),
)

var Photo = nl.List("Photo",
	nl.Fields{
		"name":        field.Text(field.Required()),
		"description": field.Text(),
		"url":         field.Text(),
	},
	backOffice,
	nl.WithDescription("圖片（v1 以 URL 表示）。"),
)
