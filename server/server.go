// Package server 組裝 CMS 的 HTTP handler：GraphQL API、auth middleware、
// field-level read middleware 與 playground。cmd/nl-server 與整合測試共用。
package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hcchien/nl/access"
	"github.com/hcchien/nl/admin"
	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/apikey"
	"github.com/hcchien/nl/graph"
	"github.com/hcchien/nl/oauth"
)

// New 建立完整的 CMS HTTP handler（/graphql + playground on /）。
// 呼叫端負責先完成 migration 與 access.Setup(client)。
func New(client *ent.Client, tokenSecret []byte) http.Handler {
	srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{
		Resolvers: &graph.Resolver{Client: client, TokenSecret: tokenSecret},
	}))
	srv.AroundFields(fieldReadMiddleware)

	gqlHandler := authMiddleware(client, tokenSecret, srv)

	mux := http.NewServeMux()
	mux.Handle("/", playground.Handler("nl CMS", "/graphql"))
	mux.Handle("/graphql", gqlHandler)
	mux.Handle("/graphql/content", contentHandler(client))
	// OAuth 2.1 authorization server（MCP clients 的「連接→登入」流程）
	(&oauth.Server{Client: client, Secret: tokenSecret}).Mount(mux)
	// Admin UI：所有操作經 in-process GraphQL（帶登入者 token），權限與 API/MCP 一致
	adminHandler := admin.New(gqlHandler, tokenSecret)
	mux.Handle("/admin", adminHandler)
	mux.Handle("/admin/", adminHandler)
	return mux
}

// Setup 完成 client 的初始化：auto migration + 權限裁決掛載（dev 預設路徑）。
func Setup(ctx context.Context, client *ent.Client) error {
	if err := client.Schema.Create(ctx); err != nil {
		return err
	}
	Attach(client)
	return nil
}

// Attach 只掛載權限裁決，不做 migration（prod 走 migrate plan/apply 時使用）。
func Attach(client *ent.Client) {
	access.Setup(client)
}

// authMiddleware 解析 Authorization: Bearer <session token | API key>，
// 將 viewer 注入 context。無效憑證視同未登入（由權限層擋下）。
func authMiddleware(client *ent.Client, secret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != "" {
			sys := auth.WithSystem(ctx)
			var u *ent.User
			if auth.IsAPIKey(token) {
				k, err := client.ApiKey.Query().
					Where(apikey.KeyHashEQ(auth.HashAPIKey(token)), apikey.ScopeEQ(apikey.ScopeCMS)).
					WithUser().
					Only(sys)
				if err == nil {
					u = k.Edges.User
				}
			} else if uid, err := auth.ParseToken(secret, token); err == nil {
				u, _ = client.User.Get(sys, uid)
			}
			if u != nil {
				ctx = auth.WithViewer(ctx, &auth.Viewer{
					ID: u.ID, Name: u.Name, Email: u.Email, Role: string(u.Role),
				})
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// fieldReadMiddleware 執行 field 層級的讀取控制（例如 User.email 僅 admin/moderator，
// 本人除外）。list 層級與 row-level 已由 ent interceptor 處理。
func fieldReadMiddleware(ctx context.Context, next graphql.Resolver) (any, error) {
	fc := graphql.GetFieldContext(ctx)
	obj := fc.Object
	if obj == "Query" || obj == "Mutation" || strings.HasPrefix(obj, "__") {
		return next(ctx)
	}
	v := auth.ViewerFrom(ctx)
	role := ""
	if v != nil {
		role = v.Role
	}
	if !access.FieldReadAllowed(obj, fc.Field.Name, role) {
		// 本人可讀自己的 User 欄位
		if v != nil && fc.Parent != nil {
			if u, ok := fc.Parent.Result.(*ent.User); ok && u != nil && u.ID == v.ID {
				return next(ctx)
			}
		}
		return nil, gqlerror.Errorf("access denied: cannot read %s.%s", obj, fc.Field.Name)
	}
	return next(ctx)
}
