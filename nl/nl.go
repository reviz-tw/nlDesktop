// Package nl 是框架的宣告式 DSL：以單一宣告定義 list 的欄位、關聯與權限。
//
// 一個 list 宣告會被展開成四種投影：
//   - ent schema（nl gen 產生，編譯期）
//   - GraphQL mutations 與 resolvers（nl gen 產生，編譯期）
//   - access 權限規則（執行期由 registry 導出）
//   - MCP schema metadata（執行期由 registry 導出）
package nl

import (
	"fmt"
	"sort"
)

// ---- Field ----

// FieldType 是欄位型別。
type FieldType string

const (
	TypeText         FieldType = "text"      // string（Postgres text）
	TypeInteger      FieldType = "integer"   // int
	TypeBoolean      FieldType = "boolean"   // bool
	TypeTimestamp    FieldType = "timestamp" // time.Time
	TypeSelect       FieldType = "select"    // enum
	TypeRichText     FieldType = "richText"  // ProseMirror doc JSON（JSONB）
	TypePassword     FieldType = "password"  // argon2id 雜湊，不出現在查詢型別
	TypeRelationship FieldType = "relationship"
)

// Field 是一個欄位宣告。以 fields 子套件的建構子產生。
type Field struct {
	Type     FieldType
	Required bool
	ReadOnly bool // Server-derived: queryable, excluded from mutation inputs and editing.
	Unique   bool
	Default  any      // select/boolean 預設值
	Enum     []string // select 選項
	Match    string   // 驗證用 regexp（text）
	Ref      string   // relationship 目標 list
	Many     bool     // relationship 是否多值
	Inverse  string   // relationship 反向欄位名（在目標 list 上），空字串表示無反向
	OrderKey string   // 非空時生成 entgql OrderField（值為 SCREAMING_SNAKE）
	Note     string   // 給人與 agent 看的說明
}

// ---- Access ----

// Rule 是一個操作的權限規則：Allowed 直接放行；OwnerScoped 放行但僅限自己建立的資料。
type Rule struct {
	Allowed     []string
	OwnerScoped []string
}

// Roles 建立直接放行的規則。
func Roles(roles ...string) Rule { return Rule{Allowed: roles} }

// OwnedBy 追加 owner-scoped 角色（需搭配 TrackCreator）。
func (r Rule) OwnedBy(roles ...string) Rule {
	r.OwnerScoped = append(r.OwnerScoped, roles...)
	return r
}

// Ops 是 list 層級四種操作的規則。零值規則表示拒絕所有人。
type Ops struct {
	Query  Rule
	Create Rule
	Update Rule
	Delete Rule
}

// FieldRule 是 field 層級規則；nil slice 表示不額外限制。
type FieldRule struct {
	Read   []string
	Create []string
	Update []string
}

// ---- List ----

// ListDef 是一個 list 的完整宣告。
type ListDef struct {
	Name         string
	Description  string
	LabelField   string
	Fields       map[string]Field
	FieldOrder   []string // 宣告順序（map 無序，生成需穩定）
	Ops          Ops
	FieldAccess  map[string]FieldRule
	TrackCreator bool // 加 createdBy 關聯（owner-scoped 規則的前提）
}

// Option 修飾 list 宣告。
type Option func(*ListDef)

// WithDescription 設定給人與 agent 看的描述。
func WithDescription(d string) Option { return func(l *ListDef) { l.Description = d } }

// WithLabelField 設定顯示欄位（預設 name，Post 常用 title）。
func WithLabelField(f string) Option { return func(l *ListDef) { l.LabelField = f } }

// WithFieldAccess 設定 field 層級權限。
func WithFieldAccess(fa map[string]FieldRule) Option {
	return func(l *ListDef) { l.FieldAccess = fa }
}

// TrackCreator 啟用 createdBy 追蹤（owner-scoped 權限的前提）。
func TrackCreator() Option { return func(l *ListDef) { l.TrackCreator = true } }

// Fields 是欄位宣告集合。
type Fields map[string]Field

// registry 收集所有宣告；lists package 的 var 初始化時註冊。
var registry []*ListDef

// List 宣告一個 list 並註冊。欄位名使用 GraphQL 命名（camelCase）。
func List(name string, fields Fields, ops Ops, opts ...Option) *ListDef {
	l := &ListDef{
		Name:       name,
		LabelField: "name",
		Fields:     map[string]Field(fields),
		Ops:        ops,
	}
	for _, opt := range opts {
		opt(l)
	}
	for fname := range l.Fields {
		l.FieldOrder = append(l.FieldOrder, fname)
	}
	sort.Strings(l.FieldOrder)
	if err := l.validate(); err != nil {
		panic(fmt.Sprintf("nl.List(%s): %v", name, err))
	}
	registry = append(registry, l)
	return l
}

// Registry 回傳所有已註冊的 lists（名稱排序，生成穩定）。
func Registry() []*ListDef {
	out := make([]*ListDef, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get 依名稱取得宣告。
func Get(name string) (*ListDef, bool) {
	for _, l := range registry {
		if l.Name == name {
			return l, true
		}
	}
	return nil, false
}

func (l *ListDef) validate() error {
	if l.Name == "" {
		return fmt.Errorf("empty list name")
	}
	ownerScoped := len(l.Ops.Query.OwnerScoped)+len(l.Ops.Update.OwnerScoped)+
		len(l.Ops.Create.OwnerScoped)+len(l.Ops.Delete.OwnerScoped) > 0
	if ownerScoped && !l.TrackCreator {
		return fmt.Errorf("owner-scoped rules require nl.TrackCreator()")
	}
	for name, f := range l.Fields {
		if f.Type == TypeRelationship && f.Ref == "" {
			return fmt.Errorf("field %s: relationship needs Ref", name)
		}
		if f.Type == TypeSelect && len(f.Enum) == 0 {
			return fmt.Errorf("field %s: select needs options", name)
		}
	}
	return nil
}
