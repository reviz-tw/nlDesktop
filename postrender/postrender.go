// Package postrender maintains Post's persisted website representation.
package postrender

import (
	"context"
	"fmt"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/photo"
	"github.com/hcchien/nl/ent/post"
	"github.com/hcchien/nl/richtext"
)

type rebuildKey struct{}

// Hook runs inside the access hook: authorization completes before image lookup
// and rendering, and JSON/HTML/version are persisted in a single DB mutation.
func Hook() ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			pm, ok := m.(*ent.PostMutation)
			if !ok || !m.Op().Is(ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne) {
				return next.Mutate(ctx, m)
			}
			if permitted, _ := ctx.Value(rebuildKey{}).(bool); permitted && auth.IsSystem(ctx) {
				return next.Mutate(ctx, m)
			}
			_, htmlSet := pm.ContentHTML()
			version, versionSet := pm.RenderVersion()
			_, versionAdded := pm.AddedRenderVersion()
			if htmlSet || pm.ContentHTMLCleared() || pm.RenderVersionCleared() || versionAdded || (versionSet && !(m.Op().Is(ent.OpCreate) && version == 0)) {
				return nil, fmt.Errorf("contentHtml and renderVersion are server-derived and read-only")
			}
			doc, changed := pm.Content()
			if pm.ContentCleared() {
				doc = nil
				changed = true
			}
			if m.Op().Is(ent.OpCreate) || changed {
				rendered, err := render(ctx, pm.Client(), doc)
				if err != nil {
					return nil, fmt.Errorf("rendering Post.content: %w", err)
				}
				pm.SetContentHTML(rendered)
				pm.SetRenderVersion(richtext.RenderVersion)
			}
			return next.Mutate(ctx, m)
		})
	}
}

func render(ctx context.Context, c *ent.Client, doc map[string]any) (string, error) {
	// Validate before walking references, including limits on nested input.
	if _, err := richtext.Render(doc, nil); err != nil {
		return "", err
	}
	photos := map[int]richtext.Photo{}
	if ids := richtext.PhotoIDs(doc); len(ids) > 0 {
		// Use the caller's viewer. A guessed Photo ID cannot reveal a restricted URL.
		rows, err := c.Photo.Query().Where(photo.IDIn(ids...)).All(ctx)
		if err != nil {
			return "", fmt.Errorf("reading referenced images: %w", err)
		}
		for _, p := range rows {
			alt := p.Description
			if alt == "" {
				alt = p.Name
			}
			photos[p.ID] = richtext.Photo{URL: p.URL, Alt: alt}
		}
	}
	return richtext.Render(doc, photos)
}

type RebuildResult struct{ Updated, Skipped int }

// Rebuild visits stale records in bounded batches. all also rebuilds current
// versions (e.g. after Photo URLs change). Only trusted maintenance may call it.
// An optimistic updatedAt check prevents overwriting a concurrent content edit;
// successful rebuilds preserve editorial updatedAt and do not alter source JSON.
func Rebuild(ctx context.Context, c *ent.Client, all bool) (RebuildResult, error) {
	result := RebuildResult{}
	if !auth.IsSystem(ctx) {
		return result, fmt.Errorf("HTML rebuild requires a system maintenance context")
	}
	lastID := 0
	for {
		q := c.Post.Query().Where(post.IDGT(lastID)).Order(ent.Asc(post.FieldID)).Limit(100)
		if !all {
			q.Where(post.Or(post.RenderVersionIsNil(), post.RenderVersionLT(richtext.RenderVersion), post.ContentHTMLIsNil()))
		}
		rows, err := q.All(ctx)
		if err != nil {
			return result, err
		}
		if len(rows) == 0 {
			return result, nil
		}
		for _, p := range rows {
			lastID = p.ID
			rendered, err := render(ctx, c, p.Content)
			if err != nil {
				return result, fmt.Errorf("Post #%d: %w", p.ID, err)
			}
			n, err := c.Post.Update().Where(post.IDEQ(p.ID), post.UpdatedAtEQ(p.UpdatedAt)).
				SetContentHTML(rendered).SetRenderVersion(richtext.RenderVersion).SetUpdatedAt(p.UpdatedAt).
				Save(context.WithValue(ctx, rebuildKey{}, true))
			if err != nil {
				return result, fmt.Errorf("Post #%d: %w", p.ID, err)
			}
			if n == 0 {
				result.Skipped++
			} else {
				result.Updated++
			}
		}
	}
}
