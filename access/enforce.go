package access

import (
	"context"
	"fmt"
	"strings"

	"entgo.io/ent/dialect/sql"
	"github.com/99designs/gqlgen/graphql"

	"github.com/hcchien/nl/auth"
	gen "github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/intercept"
	"github.com/hcchien/nl/ent/post"
	"github.com/hcchien/nl/meta"
	"github.com/hcchien/nl/nl"
	"github.com/hcchien/nl/postrender"
	"github.com/hcchien/nl/richtext"
)

// Setup 將權限裁決掛上 ent client：
//   - query interceptor：list 層級 query gate + row-level filter
//   - mutation hook：create/update/delete gate、field 層級寫入控制、
//     owner 檢查、createdBy 自動填入、密碼雜湊、Post 驗證
//
// 所有 API 表面（GraphQL、MCP）共用同一個 client，因此無法繞過。
func Setup(c *gen.Client) {
	c.Intercept(queryInterceptor())
	c.Use(mutationHook())
	c.Post.Use(postrender.Hook())
}

// Denied 表示權限不足的錯誤。
type Denied struct{ msg string }

func (d *Denied) Error() string { return d.msg }

func deniedf(format string, args ...any) error {
	return &Denied{msg: fmt.Sprintf(format, args...)}
}

func roleOf(ctx context.Context) string {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Role
	}
	return ""
}

func queryInterceptor() gen.Interceptor {
	return intercept.Func(func(ctx context.Context, q intercept.Query) error {
		if at, ok := auth.ContentReadTime(ctx); ok {
			return contentQueryFilter(q, at)
		}
		if auth.IsSystem(ctx) {
			return nil
		}
		list := q.Type()
		role := roleOf(ctx)
		if !CanOperate(list, OpQuery, role) {
			// 巢狀關聯載入（如 posts 展開 categories）對無權限的目標 list
			// 優雅降級為空結果（Keystone 語意：關聯欄位變 null / 空列表），
			// 只有 top-level 查詢才回傳 access denied。
			if isNestedLoad(ctx, list) {
				q.WhereP(func(s *sql.Selector) { s.Where(sql.ExprP("FALSE")) })
				return nil
			}
			return deniedf("access denied: role %q cannot query %s", role, list)
		}
		if OwnerScoped(list, OpQuery, role) {
			v := auth.ViewerFrom(ctx)
			pred, ok := ownerPredicate(list, v.ID)
			if !ok {
				return deniedf("access denied: owner scope not supported for %s", list)
			}
			q.WhereP(pred)
		}
		return nil
	})
}

// isNestedLoad 判斷此 ent query 是否為 GraphQL 巢狀關聯的 eager-load：
// 當下正在解析的 GraphQL 欄位若不是該 list 自己的 top-level connection 欄位，
// 即為巢狀載入（例如解析 Query.posts 時載入 Category）。
// 非 GraphQL 情境（直接使用 ent client）一律視為 top-level。
func isNestedLoad(ctx context.Context, list string) bool {
	if !graphql.HasOperationContext(ctx) {
		return false
	}
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return false
	}
	l, ok := meta.Get(list)
	if !ok {
		return false
	}
	return fc.Field.Name != l.QueryField
}

func opOf(op gen.Op) Op {
	switch {
	case op.Is(gen.OpCreate):
		return OpCreate
	case op.Is(gen.OpUpdate | gen.OpUpdateOne):
		return OpUpdate
	default:
		return OpDelete
	}
}

func mutationHook() gen.Hook {
	return func(next gen.Mutator) gen.Mutator {
		return gen.MutateFunc(func(ctx context.Context, m gen.Mutation) (gen.Value, error) {
			list := m.Type()
			if _, ok := auth.ContentReadTime(ctx); ok {
				return nil, deniedf("access denied: content:read cannot mutate")
			}
			// 資料完整性（不分 system / viewer，一律執行）：
			// 密碼以雜湊落地、richText 僅允許白名單內的 node/mark
			if um, ok := m.(*gen.UserMutation); ok {
				if pw, ok := um.Password(); ok && !auth.IsHashed(pw) {
					um.SetPassword(auth.HashPassword(pw))
				}
			}
			if err := validateRichText(list, m); err != nil {
				return nil, err
			}
			if auth.IsSystem(ctx) {
				return next.Mutate(ctx, m)
			}
			v := auth.ViewerFrom(ctx)
			role := roleOf(ctx)
			op := opOf(m.Op())

			if !CanOperate(list, op, role) {
				return nil, deniedf("access denied: role %q cannot %s %s", role, op, list)
			}

			// field 層級寫入控制
			for _, f := range m.Fields() {
				if !FieldWriteAllowed(list, snakeToCamel(f), op, role) {
					return nil, deniedf("access denied: role %q cannot %s field %s.%s", role, op, list, snakeToCamel(f))
				}
			}

			// row-level：owner-scoped 角色只能改自己建立的資料
			if OwnerScoped(list, op, role) {
				owned, single, err := ownedExists(auth.WithSystem(ctx), m, v.ID)
				if err != nil {
					return nil, err
				}
				if !single {
					return nil, deniedf("access denied: bulk %s not allowed for role %q", op, role)
				}
				if !owned {
					return nil, deniedf("access denied: role %q can only %s own %s items", role, op, list)
				}
			}

			// createdBy 自動填入（nl.TrackCreator 的 lists）
			if m.Op().Is(gen.OpCreate) && v != nil {
				setCreator(m, v.ID)
			}

			// Post 商業驗證（對照 Keystone validateInput）
			if pm, ok := m.(*gen.PostMutation); ok {
				if err := validatePost(ctx, pm); err != nil {
					return nil, err
				}
			}

			return next.Mutate(ctx, m)
		})
	}
}

// validatePost：state 非 draft 時 publishTime 必填。
func validatePost(ctx context.Context, pm *gen.PostMutation) error {
	state, changed := pm.State()
	if !changed || state == post.StateDraft {
		return nil
	}
	if _, ok := pm.PublishTime(); ok {
		return nil
	}
	if id, exists := pm.ID(); exists {
		old, err := pm.Client().Post.Get(auth.WithSystem(ctx), id)
		if err == nil && old.PublishTime != nil {
			return nil
		}
	}
	return fmt.Errorf("需要填入發布時間（state 為 %s 時 publishTime 必填）", state)
}

// validateRichText 對 mutation 中變更的 richText 欄位執行 node/mark 白名單驗證。
func validateRichText(list string, m gen.Mutation) error {
	def, ok := nl.Get(list)
	if !ok {
		return nil
	}
	for _, fname := range def.FieldOrder {
		if def.Fields[fname].Type != nl.TypeRichText {
			continue
		}
		v, exists := m.Field(camelToSnake(fname))
		if !exists {
			continue
		}
		doc, _ := v.(map[string]any)
		if err := richtext.Validate(doc); err != nil {
			return fmt.Errorf("%s.%s: %w", list, fname, err)
		}
	}
	return nil
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
