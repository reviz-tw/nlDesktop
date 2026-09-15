# 網站公開內容 API

`GET /graphql/content` 與 `POST /graphql/content` 使用獨立的唯讀 GraphQL schema。
與後台 `/graphql` 共用同一個 Go server、資料庫及 `access` 資料權限層。

## 簽發網站金鑰

以 admin 或 moderator 身分，向原本 `/graphql` 傳送：

```graphql
mutation {
  createContentApiKey(name: "website production") {
    id
    name
    key
  }
}
```

將 `key` 存在網站 server 的 secret 設定，並保存 `id` 供日後撤銷。明文只回傳一次，
CMS 只儲存 SHA-256 雜湊。金鑰 scope 固定為 `content:read`，沒有 CMS 角色權限。
它不能用來讀取原本 `/graphql` 的後台資料、寫入文章或簽發其他金鑰。

內容 endpoint 只接受這種專用金鑰；一般 CMS API key、session token 和匿名請求均回傳 HTTP 401。
不要把網站金鑰放進瀏覽器 bundle，也不要把它放進 URL。僅由網站 server 使用：

```http
POST /graphql/content
Authorization: Bearer <content-api-key>
Content-Type: application/json
```

```json
{
  "query": "query Article($id: ID!) { post(id: $id) { id title subtitle contentHtml renderVersion publishTime heroImage { url description } writers { name bio image { url } } categories { name slug } relatedPosts { id title } } }",
  "variables": { "id": "1" }
}
```

`Post` 目前沒有 slug，因此單篇使用 ID。

## 查詢與分頁

```graphql
query {
  posts(first: 20, offset: 0, categorySlug: "environment") {
    totalCount
    hasMore
    items {
      id
      title
      publishTime
      section { slug name }
      tags { id name }
    }
  }
  sections { id slug name }
  categories { id slug name section { id name } }
  tags { id name isFeatured }
}
```

- `posts` 支援 `sectionSlug`、`categorySlug`、`tagId` 篩選，條件以 AND 合併。
- `posts` 預設每頁 20；分類／標籤清單預設每頁 50。
- 全部列表接受 `first`（1–50）和 `offset`（0–10000）；超出範圍會拒絕。
- 文章按 `publishTime DESC, id DESC` 排序。offset 分頁在文章發布或下架時可能位移。
- `relatedPosts` 為公開文章摘要，無遞迴展開。
- `contentHtml` 使用已儲存的網站 HTML；不在讀取時重新渲染內文。
- `brief` 是公開前言，仍沿用 CMS ProseMirror JSON；內文原始 `content` 不暴露。
- 完整欄位白名單見 `contentgraph/schema.graphqls`；授權後可用 introspection 查詢。

## 公開內容規則

資料庫查詢必須同時符合 `state = published` 與 `publishTime <= 請求時間`。
沒有發布時間、未到時間、draft、scheduled、invisible、archived 的文章均不可讀。
排程時間已到但狀態仍是 scheduled 的文章不會自動公開；排程發布程序須先轉為 published。

這套規則在資料層統一套用於單篇 ID、列表、totalCount、相關文章和巢狀關聯。
查不到與無權讀取的單篇一律回傳 `post: null`。
分類、標籤、作者必須被公開文章引用；Section 可由公開文章直接引用，或透過公開文章的 Category 引用。
可獨立讀取的圖片僅限公開文章主圖與其作者頭像；內文圖片由 `contentHtml` 提供。
目前 CMS 沒有作者或圖片的額外可見性欄位：引用於公開文章即視為上述資訊可公開。

Schema 沒有 User、密碼、建立者、原始內文、Node 通用入口或任何 Mutation。
新增 CMS 欄位不會自動加入公開 schema。HTTP 認證完成後不使用 system context 讀取內容。

## 撤銷與輪替

以 admin/moderator 身分向 `/graphql` 傳送：

```graphql
mutation {
  revokeContentApiKey(id: "123")
}
```

成功刪除回傳 true，已撤銷或不存在回傳 false；這個操作不會撤銷一般 CMS API key。
下一個請求即失效。每次請求也檢查簽發者仍為 admin/moderator；帳號刪除或降權後金鑰不再可用。
金鑰目前沒有自動到期日。輪替方式為先簽發新金鑰、更新網站 server，再撤銷舊金鑰。
既有 API key 經 migration 預設 scope 為 `cms`，用途不變。

## 快取與限制

目前 API 一律回傳 `Cache-Control: private, no-store` 和 `Vary: Authorization`，
避免共用快取繞過認證與延長已撤銷金鑰的有效期。
網站仍可自行快取 SSR／SSG 的公開頁面，但發布、下架和更新時需由網站清除頁面快取。
本次不包含網站端 CDN 或頁面快取失效通知。

僅啟用 GET／JSON POST，沒有 websocket、上傳或 subscription。
POST body 上限 64 KiB、parser token 上限 10000、查詢 complexity 上限 2000；查詢 context 限時 10 秒。

## 開發與部署

```sh
go run ./cmd/nl gen
```

此命令會同時更新後台與公開內容兩份 GraphQL 程式碼。
正式環境先套用新增 `api_keys.scope` 的 migration，再啟動新版 server；local 自動 migration 路徑也支援。
權限整合測試位於 `e2e/content_api_test.go`，必須使用專用測試 PostgreSQL，測試會重建測試資料庫 schema。
