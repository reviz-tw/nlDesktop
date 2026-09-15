package access

import (
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/hcchien/nl/ent/author"
	"github.com/hcchien/nl/ent/category"
	"github.com/hcchien/nl/ent/intercept"
	"github.com/hcchien/nl/ent/photo"
	"github.com/hcchien/nl/ent/post"
	"github.com/hcchien/nl/ent/predicate"
	"github.com/hcchien/nl/ent/section"
	"github.com/hcchien/nl/ent/tag"
)

// PublicPost is shared by direct article reads and every relationship predicate.
// Scheduled, future, undated, invisible, archived and draft rows stay private.
func PublicPost(at time.Time) predicate.Post {
	return post.And(post.StateEQ(post.StatePublished), post.PublishTimeLTE(at))
}

// Apply before all other role gates, including system context. SQL-level filters
// cover IDs, counts and eager loads, and cannot be overridden by caller filters.
func contentQueryFilter(q intercept.Query, at time.Time) error {
	visible := PublicPost(at)
	switch q.Type() {
	case "Post":
		q.WhereP(visible)
	case "Author":
		q.WhereP(author.HasPostsWith(visible))
	case "Category":
		q.WhereP(category.HasPostsWith(visible))
	case "Tag":
		q.WhereP(tag.HasPostsWith(visible))
	case "Section":
		q.WhereP(section.Or(section.HasPostsWith(visible), section.HasCategoriesWith(category.HasPostsWith(visible))))
	case "Photo":
		// Photo has no inverse edges. Only public heroes and public authors'
		// portraits are independently readable; body images live in contentHtml.
		q.WhereP(func(s *sql.Selector) {
			p := sql.Dialect(s.Dialect()).Select(post.HeroImageColumn).From(sql.Table(post.Table))
			visible(p)
			a := sql.Dialect(s.Dialect()).Select(author.ImageColumn).From(sql.Table(author.Table))
			author.HasPostsWith(visible)(a)
			s.Where(sql.Or(sql.In(s.C(photo.FieldID), p), sql.In(s.C(photo.FieldID), a)))
		})
	default:
		return deniedf("access denied: content:read cannot query %s", q.Type())
	}
	return nil
}
