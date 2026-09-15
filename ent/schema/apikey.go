package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ApiKey 綁定特定使用者的長效憑證（供 MCP / server-to-server）。
// 不暴露於 GraphQL schema，僅由 createApiKey mutation 操作。
type ApiKey struct {
	ent.Schema
}

func (ApiKey) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (ApiKey) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("scope").NamedValues("CMS", "cms", "ContentRead", "content:read").Default("cms"),
		field.String("name").
			NotEmpty(),
		// 只存 SHA-256 雜湊，明文只在簽發當下回傳一次
		field.String("key_hash").
			NotEmpty().
			Unique(),
	}
}

func (ApiKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).
			Unique().
			Required(),
	}
}

func (ApiKey) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.Skip(entgql.SkipAll),
	}
}
