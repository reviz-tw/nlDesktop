package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/content"
	"github.com/hcchien/nl/contentgraph"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/apikey"
)

func contentHandler(client *ent.Client) http.Handler {
	complexity := contentgraph.ComplexityRoot{}
	pageCost := func(child, first, offset int) int {
		if first < 1 || first > 50 {
			return 2001
		}
		return 1 + first*child
	}
	complexity.Query.Posts = func(child, first, offset int, section, category *string, tag *int) int {
		return pageCost(child, first, offset)
	}
	complexity.Query.Sections = pageCost
	complexity.Query.Categories = pageCost
	complexity.Query.Tags = pageCost
	srv := handler.New(contentgraph.NewExecutableSchema(contentgraph.Config{
		Resolvers: &contentgraph.Resolver{Content: &content.Service{Client: client}}, Complexity: complexity,
	}))
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](128))
	srv.Use(extension.Introspection{})
	srv.Use(extension.FixedComplexityLimit(2000))
	srv.SetParserTokenLimit(10000)
	srv.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		var gqlErr *gqlerror.Error
		if errors.As(err, &gqlErr) || errors.Is(err, content.ErrWindow) || errors.Is(err, content.ErrUnauthorized) {
			return graphql.DefaultErrorPresenter(ctx, err)
		}
		log.Printf("content query failed: %v", err)
		return graphql.DefaultErrorPresenter(ctx, errors.New("content temporarily unavailable"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Shared caches must not bypass authentication or retain withdrawn posts.
		// Websites may independently cache their rendered pages with invalidation.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Authorization")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !bearer || !auth.IsAPIKey(token) || len(token) != 52 {
			contentUnauthorized(w)
			return
		}
		// System context is confined to credential lookup, never content reads.
		key, err := client.ApiKey.Query().Where(apikey.KeyHashEQ(auth.HashAPIKey(token)), apikey.ScopeEQ(apikey.ScopeContentRead)).WithUser().Only(auth.WithSystem(ctx))
		if err != nil || key.Edges.User == nil || (key.Edges.User.Role != "admin" && key.Edges.User.Role != "moderator") {
			contentUnauthorized(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		srv.ServeHTTP(w, r.WithContext(auth.WithContentReader(ctx)))
	})
}

func contentUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="content", scope="content:read"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"message": "content:read API key required"}}})
}
