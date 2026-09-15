// Package field 提供 nl DSL 的欄位建構子。
package field

import "github.com/hcchien/nl/nl"

// Text 文字欄位。
func Text(opts ...Opt) nl.Field { return build(nl.Field{Type: nl.TypeText}, opts) }

// Integer 整數欄位。
func Integer(opts ...Opt) nl.Field { return build(nl.Field{Type: nl.TypeInteger}, opts) }

// Boolean 布林欄位。
func Boolean(opts ...Opt) nl.Field { return build(nl.Field{Type: nl.TypeBoolean}, opts) }

// Timestamp 時間欄位。
func Timestamp(opts ...Opt) nl.Field { return build(nl.Field{Type: nl.TypeTimestamp}, opts) }

// Select 單選欄位（enum）。
func Select(options []string, opts ...Opt) nl.Field {
	return build(nl.Field{Type: nl.TypeSelect, Enum: options}, opts)
}

// RichText 富文字欄位（ProseMirror doc JSON）。
func RichText(opts ...Opt) nl.Field { return build(nl.Field{Type: nl.TypeRichText}, opts) }

// Password 密碼欄位（argon2id 雜湊，不出現在查詢型別與 filter）。
func Password(opts ...Opt) nl.Field { return build(nl.Field{Type: nl.TypePassword}, opts) }

// Relationship 單值關聯。
func Relationship(ref string, opts ...Opt) nl.Field {
	return build(nl.Field{Type: nl.TypeRelationship, Ref: ref}, opts)
}

// RelationshipMany 多值關聯。
func RelationshipMany(ref string, opts ...Opt) nl.Field {
	return build(nl.Field{Type: nl.TypeRelationship, Ref: ref, Many: true}, opts)
}

// Opt 修飾欄位。
type Opt func(*nl.Field)

// Required 必填。
func Required() Opt { return func(f *nl.Field) { f.Required = true } }

// ReadOnly marks a server-derived field that clients may only read.
func ReadOnly() Opt { return func(f *nl.Field) { f.ReadOnly = true } }

// Unique 唯一。
func Unique() Opt { return func(f *nl.Field) { f.Unique = true } }

// Default 預設值（select 用字串、boolean 用 bool）。
func Default(v any) Opt { return func(f *nl.Field) { f.Default = v } }

// Match 以 regexp 驗證（text）。
func Match(re string) Opt { return func(f *nl.Field) { f.Match = re } }

// Orderable 生成排序欄位，key 為 SCREAMING_SNAKE（如 PUBLISH_TIME）。
func Orderable(key string) Opt { return func(f *nl.Field) { f.OrderKey = key } }

// Inverse 設定關聯的反向欄位名（於目標 list 上）。
func Inverse(name string) Opt { return func(f *nl.Field) { f.Inverse = name } }

// Note 給人與 agent 看的說明。
func Note(n string) Opt { return func(f *nl.Field) { f.Note = n } }

func build(f nl.Field, opts []Opt) nl.Field {
	for _, o := range opts {
		o(&f)
	}
	return f
}
