// Package content provides the public-content read service shared by API adapters.
// All reads use the caller's content identity and the access layer's SQL policy.
package content

import (
	"context"
	"errors"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/author"
	"github.com/hcchien/nl/ent/category"
	"github.com/hcchien/nl/ent/post"
	"github.com/hcchien/nl/ent/section"
	"github.com/hcchien/nl/ent/tag"
)

var ErrWindow = errors.New("first must be 1..50 and offset must be 0..10000")
var ErrUnauthorized = errors.New("content:read required")

var postFields = []string{post.FieldID, post.FieldTitle, post.FieldSubtitle,
	post.FieldBrief, post.FieldContentHTML, post.FieldRenderVersion, post.FieldPublishTime,
	post.FieldUpdatedAt, post.FieldOtherByline}

type Service struct{ Client *ent.Client }

type Filter struct {
	SectionSlug, CategorySlug *string
	TagID                     *int
}

func window(ctx context.Context, first, offset int) error {
	if _, ok := auth.ContentReadTime(ctx); !ok {
		return ErrUnauthorized
	}
	if first < 1 || first > 50 || offset < 0 || offset > 10000 {
		return ErrWindow
	}
	return nil
}

func (s *Service) posts() *ent.PostQuery {
	// Eager-load public relationships in batches; scalar reads use postFields
	// and never load the creator record or raw editorial content.
	return s.Client.Post.Query().
		WithHeroImage().WithSection().WithCategories(func(q *ent.CategoryQuery) { q.Order(ent.Asc(category.FieldID)).WithSection() }).
		WithTags(func(q *ent.TagQuery) { q.Order(ent.Asc(tag.FieldSortOrder), ent.Asc(tag.FieldID)) }).
		WithWriters(func(q *ent.AuthorQuery) { q.Order(ent.Asc(author.FieldID)).WithImage() }).
		WithRelatedPosts(func(q *ent.PostQuery) {
			q.Select(post.FieldID, post.FieldTitle, post.FieldSubtitle, post.FieldPublishTime).Order(ent.Desc(post.FieldPublishTime), ent.Desc(post.FieldID))
		})
}

func (s *Service) Post(ctx context.Context, id int) (*ent.Post, error) {
	if err := window(ctx, 1, 0); err != nil {
		return nil, err
	}
	p, err := s.posts().Select(postFields...).Where(post.IDEQ(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return p, err
}

func (s *Service) Posts(ctx context.Context, first, offset int, f Filter) ([]*ent.Post, int, error) {
	if err := window(ctx, first, offset); err != nil {
		return nil, 0, err
	}
	q := s.posts()
	if f.SectionSlug != nil {
		q.Where(post.HasSectionWith(section.SlugEQ(*f.SectionSlug)))
	}
	if f.CategorySlug != nil {
		q.Where(post.HasCategoriesWith(category.SlugEQ(*f.CategorySlug)))
	}
	if f.TagID != nil {
		q.Where(post.HasTagsWith(tag.IDEQ(*f.TagID)))
	}
	count, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Select(postFields...).Order(ent.Desc(post.FieldPublishTime), ent.Desc(post.FieldID)).Limit(first).Offset(offset).All(ctx)
	return rows, count, err
}

func (s *Service) Sections(ctx context.Context, first, offset int) ([]*ent.Section, error) {
	if err := window(ctx, first, offset); err != nil {
		return nil, err
	}
	return s.Client.Section.Query().Order(ent.Asc(section.FieldID)).Limit(first).Offset(offset).All(ctx)
}
func (s *Service) Categories(ctx context.Context, first, offset int) ([]*ent.Category, error) {
	if err := window(ctx, first, offset); err != nil {
		return nil, err
	}
	return s.Client.Category.Query().WithSection().Order(ent.Asc(category.FieldID)).Limit(first).Offset(offset).All(ctx)
}
func (s *Service) Tags(ctx context.Context, first, offset int) ([]*ent.Tag, error) {
	if err := window(ctx, first, offset); err != nil {
		return nil, err
	}
	return s.Client.Tag.Query().Order(ent.Asc(tag.FieldSortOrder), ent.Asc(tag.FieldID)).Limit(first).Offset(offset).All(ctx)
}
