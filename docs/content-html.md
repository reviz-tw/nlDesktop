# Website article HTML

`Post.content` remains the editable ProseMirror JSON source. `Post.contentHtml`
contains persisted website HTML; `Post.renderVersion` identifies the renderer
contract (currently `1`). These two derived fields are queryable but absent from
GraphQL create/update inputs, and are not editable in the CMS. MCP metadata marks
them `readOnly`.

```graphql
query Article($id: ID) {
  posts(first: 1, where: { id: $id }) {
    edges {
      node { id title contentHtml renderVersion }
    }
  }
}
```

The query uses the existing session/API-key authorization and row-level rules.
Adding HTML output does not introduce anonymous access or change publication
permissions. A website server should use its authorized CMS integration and apply
its publication selection rules.

## Write and read behavior

The ent Post mutation hook runs after access checks. On create, content change, or
content clear, Go generates HTML and stores JSON/HTML/version in the same database
write. A rendering failure aborts the write; a title/status-only edit reuses the
stored HTML. An empty document produces an empty string. GraphQL reads return the
stored value, without invoking Tiptap, Node, or a converter endpoint.

`contentHtml` is an HTML fragment. Render it inside the site's article container,
for example `<article class="article-content">…</article>`. The website supplies
its own CSS. Regular HTML does not need an editor or document parser. Interactive
slideshow controls can progressively enhance the already-rendered images.

The existing converter `/to-html` endpoint remains an editor-schema round-trip
utility. Persisted website HTML uses `richtext/render.go`, which has a separate,
versioned semantic output contract. When adding editor nodes or marks, extend the
website renderer and its coverage tests together.

## Stable markup contract

| Content | Markup / classes |
| --- | --- |
| Text / headings | `p`, `h1`–`h6`, `strong`, `em`, `s`, `u`, `sub`, `sup`, `a` |
| Lists / quotes | `ul`, `ol`, `li`, `blockquote` |
| Code | `pre > code`; optional `language-*` class |
| Authored alignment | `article-align-left`, `article-align-center`, `article-align-right`, `article-align-justify` |
| Image | `img` with `src`, `alt`, optional `title` / `data-photo-id`, lazy loading |
| Info box | `aside.article-info-box`, optional `p.article-info-box-title` |
| Slideshow | `figure.article-slideshow[data-type="slideshow"] > div.article-slides > figure.article-slide`; optional `figcaption` |
| YouTube | `figure.article-video > iframe` using a canonical youtube-nocookie.com embed URL |
| Raw embed | `figure.article-embed > iframe`; escaped `srcdoc`, `sandbox="allow-scripts"`; optional `figcaption` |
| Missing media | `article-media-unavailable` placeholder |

No site theme CSS or arbitrary editor `style` attributes are stored in the output.
Changing site typography, spacing, colors or responsive CSS does not require
regenerating articles. Authored alignment is preserved as classes. The website's
CSP and iframe rules must allow the media it intentionally supports; retain the
sandbox attribute on raw embeds, without adding `allow-same-origin`.

Text and attribute values are escaped. Ordinary links/images allow HTTP(S) or
relative URLs (links also allow mailto/tel); executable URL schemes and arbitrary
event handlers are not copied. Raw embed HTML stays in an isolated iframe rather
than the article DOM.

Photo references are resolved on the server under the writing caller's read
permissions. Existing Photo IDs generate real `img` elements; missing images get
an explicit placeholder. Photo URLs/alt text are snapshots: updating a Photo
alone requires re-saving the referencing article or running `render rebuild
--all`. There is no image fetch by the website renderer at read time.

## Migration and rebuilding

Development startup applies the additive DB migration automatically. For
versioned deployments, run the normal migration step before starting the new
server:

```sh
./nl-server migrate up
./nl-server render rebuild
```

`render rebuild` processes up to 100 records per batch, selecting missing HTML or
older renderer versions. It preserves original content and editorial `updatedAt`,
and skips records edited concurrently. Run it again if concurrent edits were
reported; a currently edited article is normally rendered by its write hook.
Older rows may have `contentHtml: null` / `renderVersion: 0` until backfilled.

When the HTML structure, class contract, or escaping policy changes, increment
`richtext.RenderVersion`, deploy, and run the rebuild command. To refresh all
records using the current renderer (including changed Photo URLs), use:

```sh
./nl-server render rebuild --all
```

Rebuilding database HTML does not purge external website/page/CDN caches. The
website's normal revalidation or cache invalidation process must refresh any
already-cached pages separately.
