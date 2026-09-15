// Package meta 提供 lists 的 schema metadata，供 MCP server 的 schema 探索
// tools 與動態 query 組裝使用。內容於執行期從 nl DSL registry 導出。
package meta

import (
	"fmt"
	"strings"
	"sync"

	"github.com/hcchien/nl/nl"
)

// Field 是一個欄位的描述。Name 使用 GraphQL 命名（camelCase）。
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	ReadOnly bool     `json:"readOnly,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Ref      string   `json:"ref,omitempty"`
	Many     bool     `json:"many,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// List 是一個 list 的描述。
type List struct {
	Name        string   `json:"name"`
	QueryField  string   `json:"queryField"`
	LabelField  string   `json:"labelField"`
	Description string   `json:"description"`
	OrderFields []string `json:"orderFields"`
	Fields      []Field  `json:"fields"`
}

var (
	buildOnce sync.Once
	all       []List
	byName    map[string]*List
)

// All 回傳所有 lists 的 metadata。
func All() []List {
	buildOnce.Do(build)
	return all
}

// Get 依名稱取得 list metadata（不分大小寫）。
func Get(name string) (*List, bool) {
	buildOnce.Do(build)
	l, ok := byName[strings.ToLower(name)]
	return l, ok
}

func build() {
	byName = map[string]*List{}
	for _, def := range nl.Registry() {
		l := List{
			Name:        def.Name,
			QueryField:  pluralize(lcFirst(def.Name)),
			LabelField:  def.LabelField,
			Description: def.Description,
			OrderFields: []string{"CREATED_AT", "UPDATED_AT"},
		}
		for _, fname := range def.FieldOrder {
			f := def.Fields[fname]
			if f.Type == nl.TypePassword {
				continue // 永不暴露
			}
			l.Fields = append(l.Fields, Field{
				Name:     fname,
				Type:     string(f.Type),
				Required: f.Required,
				ReadOnly: f.ReadOnly,
				Enum:     f.Enum,
				Ref:      f.Ref,
				Many:     f.Many,
				Note:     f.Note,
			})
			if f.OrderKey != "" {
				l.OrderFields = append(l.OrderFields, f.OrderKey)
			}
		}
		if def.TrackCreator {
			l.Fields = append(l.Fields, Field{
				Name: "createdBy", Type: "relationship", Ref: "User",
				Note: "由 server 自動填入",
			})
		}
		l.Fields = append(l.Fields,
			Field{Name: "createdAt", Type: "timestamp"},
			Field{Name: "updatedAt", Type: "timestamp"},
		)
		all = append(all, l)
	}
	for i := range all {
		byName[strings.ToLower(all[i].Name)] = &all[i]
	}
}

// Selection 組出該 list 的 GraphQL selection set：
// 純量欄位直接選取，關聯欄位選 {id labelField}。
func (l *List) Selection() string {
	var b strings.Builder
	b.WriteString("id")
	for _, f := range l.Fields {
		b.WriteString(" ")
		if f.Type != "relationship" {
			b.WriteString(f.Name)
			continue
		}
		label := "name"
		if ref, ok := Get(f.Ref); ok {
			label = ref.LabelField
		}
		fmt.Fprintf(&b, "%s{id %s}", f.Name, label)
	}
	return b.String()
}

// MutationHints 說明 create/update input 的關聯欄位命名慣例（entgql 生成規則）。
func (l *List) MutationHints() []string {
	var hints []string
	for _, f := range l.Fields {
		if f.Type != "relationship" || f.Name == "createdBy" {
			continue
		}
		singular := singularize(f.Name)
		if f.Many {
			hints = append(hints, fmt.Sprintf("create 用 %sIDs: [ID!]；update 用 add%sIDs / remove%sIDs", singular, upperFirst(singular), upperFirst(singular)))
		} else {
			hints = append(hints, fmt.Sprintf("create/update 用 %sID: ID；update 可用 clear%s: true 清除", f.Name, upperFirst(f.Name)))
		}
	}
	return hints
}

// pluralize 與 singularize 只需覆蓋 list/欄位命名的常見情形（與 entgql 慣例一致）。
func pluralize(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && !strings.HasSuffix(s, "ay") && !strings.HasSuffix(s, "ey") && !strings.HasSuffix(s, "oy") && !strings.HasSuffix(s, "uy"):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh"):
		return s + "es"
	default:
		return s + "s"
	}
}

func singularize(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "es") && (strings.HasSuffix(s, "xes") || strings.HasSuffix(s, "ses") || strings.HasSuffix(s, "ches") || strings.HasSuffix(s, "shes")):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s"):
		return s[:len(s)-1]
	default:
		return s
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lcFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
