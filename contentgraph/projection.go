package contentgraph

import (
	"github.com/hcchien/nl/contentgraph/model"
	"github.com/hcchien/nl/ent"
)

// Explicit projection: adding an ent/CMS field never exposes it on this API.
func publicPost(p *ent.Post) *model.PublicPost {
	if p == nil {
		return nil
	}
	out := &model.PublicPost{ID: p.ID, Title: p.Title, Subtitle: p.Subtitle, Brief: p.Brief,
		ContentHTML: p.ContentHTML, RenderVersion: p.RenderVersion, PublishTime: *p.PublishTime,
		UpdatedAt: p.UpdatedAt, OtherByline: p.OtherByline, HeroImage: publicPhoto(p.Edges.HeroImage),
		Section: publicSection(p.Edges.Section), Categories: []*model.PublicCategory{}, Tags: []*model.PublicTag{},
		Writers: []*model.PublicAuthor{}, RelatedPosts: []*model.PublicPostSummary{}}
	for _, row := range p.Edges.Categories {
		out.Categories = append(out.Categories, publicCategory(row))
	}
	for _, row := range p.Edges.Tags {
		out.Tags = append(out.Tags, publicTag(row))
	}
	for _, row := range p.Edges.Writers {
		out.Writers = append(out.Writers, &model.PublicAuthor{ID: row.ID, Name: row.Name, Bio: row.Bio, Image: publicPhoto(row.Edges.Image)})
	}
	for _, row := range p.Edges.RelatedPosts {
		out.RelatedPosts = append(out.RelatedPosts, &model.PublicPostSummary{ID: row.ID, Title: row.Title, Subtitle: row.Subtitle, PublishTime: *row.PublishTime})
	}
	return out
}
func publicPhoto(p *ent.Photo) *model.PublicPhoto {
	if p == nil {
		return nil
	}
	return &model.PublicPhoto{ID: p.ID, Name: p.Name, Description: p.Description, URL: p.URL}
}
func publicSection(s *ent.Section) *model.PublicSection {
	if s == nil {
		return nil
	}
	return &model.PublicSection{ID: s.ID, Slug: s.Slug, Name: s.Name, Description: s.Description}
}
func publicCategory(c *ent.Category) *model.PublicCategory {
	if c == nil {
		return nil
	}
	return &model.PublicCategory{ID: c.ID, Slug: c.Slug, Name: c.Name, Description: c.Description, Section: publicSection(c.Edges.Section)}
}
func publicTag(t *ent.Tag) *model.PublicTag {
	if t == nil {
		return nil
	}
	return &model.PublicTag{ID: t.ID, Name: t.Name, Brief: t.Brief, IsFeatured: t.IsFeatured}
}
