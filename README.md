# nl — AI 時代的 Headless CMS 框架（Go）

Keystone.js 式的宣告式 CMS 框架：定義 lists/fields，框架處理 DB migration、
GraphQL API、三層權限控管，並附**獨立部署的 MCP server** 讓 LLM agent
以與人類使用者完全相同的權限操作資料。

Schema 參考 [e-info CMS](https://github.com/...)（Keystone 6）的核心 lists 實作：
`User`（四種角色）、`Post`、`Author`、`Section`、`Category`、`Tag`、`Photo`。

整體設計與 roadmap 見 [PLAN.md](PLAN.md)。

## 架構

```
schema 宣告（ent/schema/*.go）
   │  go generate ./ent（ent + entgql codegen）
   ▼
ent client / GraphQL schema / Relay connections / WhereInputs
   │
   ├── nl-server：GraphQL API + auth + auto migration（權限唯一關卡）
   │      └── access/：list 層級（interceptor）+ row-level filter + field 層級（hook/middleware）
   └── nl-mcp：MCP server（獨立 binary），經 GraphQL API + API key 操作，不碰 DB
```

## Quickstart

需求：Go 1.26+、PostgreSQL。

```bash
createdb nl_dev
go run ./cmd/nl-server seed     # migration + 範例資料
go run ./cmd/nl-server          # http://localhost:8080（/ 是 playground）
```

Seed 帳號（密碼皆為 `password123`）：

| 帳號 | 角色 | 權限摘要 |
|---|---|---|
| admin@example.com | admin | 全部，含 delete |
| moderator@example.com | moderator | 管理內容與使用者，不可 delete |
| editor@example.com | editor | 只能查詢/修改**自己建立**的 Post |
| contributor@example.com | contributor | 同上，且**不可修改 Post.state**（不能發佈） |

```bash
# 登入取得 token
curl -s localhost:8080/graphql -H 'Content-Type: application/json' \
  -d '{"query":"mutation{login(email:\"editor@example.com\",password:\"password123\"){token}}"}'

# 帶 token 查詢（editor 只會看到自己建立的文章）
curl -s localhost:8080/graphql -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"query":"{posts(first:10){edges{node{id title state}}}}"}'
```

## 權限模型（access/rules.go）

三層規則，全部在 ent 資料層裁決，GraphQL 與 MCP 共用同一關卡：

1. **List operation**：query/create/update/delete × 角色。
2. **Row-level filter**（OwnerScoped）：editor/contributor 對 Post 的 query/update
   自動加上 `createdBy = 自己` 的 WHERE 條件。
3. **Field-level**：
   - 寫入：mutation hook 檢查變更欄位（例：contributor 改 `Post.state` → 拒絕）。
   - 讀取：GraphQL field middleware（例：`User.email` 僅 admin/moderator，本人除外）。

## MCP server

兩種認證模式，權限視圖都等於操作者本人：

**stdio + API key**（本機開發、自動化）：

```bash
# 先在 CMS 簽發 API key：mutation { createApiKey(name: "my-agent") { key } }
NL_API_KEY=nlk_xxx go run ./cmd/nl-mcp
claude mcp add nl-cms --env NL_API_KEY=nlk_xxx -- go run ./cmd/nl-mcp
```

**HTTP + OAuth 2.1**（多人共用的 remote MCP）：

```bash
go run ./cmd/nl-mcp -http :8081    # 不需要 API key
```

使用者從 Claude Desktop / claude.ai 加上 `http://<host>:8081` 這個 remote MCP 時，
client 會依 MCP 授權規格自動走 OAuth：發現 CMS（authorization server）→
dynamic client registration → 跳轉 CMS 登入頁 → PKCE 換 token。
**多人共用同一個 nl-mcp URL，各自登入、權限各自獨立**。CMS 端 endpoints：
`/.well-known/oauth-authorization-server`、`/oauth/register`、`/oauth/authorize`、
`/oauth/token`（v1 未含 refresh token，access token 效期 7 天）。

Tools：`list_schemas`、`describe_list`（含欄位、權限規則、mutation input 慣例）、
`query_items`（filter/排序/cursor 分頁）、`get_item`、`create_item`、`update_item`、
`delete_item`，以及進階用的 `graphql`。

權限語意：巢狀關聯展開跟隨目標 list 的 query 權限，無權限時該欄位優雅降級為
null / 空列表（Keystone 語意）；top-level 查詢無權限則回傳 access denied。

## 增減 lists/fields

schema 的唯一真相來源是 [lists/lists.go](lists/lists.go) 的宣告——欄位、關聯、
三層權限都在同一個宣告裡：

```go
var Post = nl.List("Post",
    nl.Fields{
        "title": field.Text(field.Required()),
        "tags":  field.RelationshipMany("Tag", field.Inverse("posts")),
        ...
    },
    nl.Ops{
        Query:  managers.OwnedBy(RoleEditor, RoleContributor), // row-level filter
        Delete: admins,
        ...
    },
    nl.TrackCreator(),  // createdBy 自動追蹤
    nl.WithFieldAccess(map[string]nl.FieldRule{
        "state": {Update: []string{RoleAdmin, RoleModerator, RoleEditor}},
    }),
)
```

改完宣告後：

```bash
go run ./cmd/nl gen   # 展開 ent schema / GraphQL mutations / resolvers / owner helpers，
                      # 並自動執行 entc 與 gqlgen
```

重啟 `nl-server` 即自動套用 DB migration。權限規則與 MCP metadata 在執行期
直接從宣告導出，不需要另外維護。

## Rich text（Tiptap / ProseMirror）

`field.RichText()` 欄位以 **ProseMirror doc JSON** 存於 JSONB（編輯器中立，
未來換編輯器不換資料），GraphQL 介面為 `JSON` scalar。三個組件：

- **[converter/](converter/)**：Node 轉換服務（`npm start`，port 8082）。
  `HTML/Markdown → PM JSON`、`PM JSON → HTML`。schema（[converter/schema.mjs](converter/schema.mjs)）
  是唯一真相來源，未來 Admin UI 的 Tiptap 編輯器直接共用。
  已含 custom blocks：`slideshow`（輪播，attrs 存 Photo id）、`infoBox`（巢狀富文字）、
  `embed`（嵌入碼）、youtube（官方 extension）。HTML 形態用 `data-type` 慣例：
  `<div data-type="slideshow" data-photo-ids="1,2,3">`。
- **[richtext/](richtext/richtext.go)**（Go）：node/mark 白名單驗證（在 ent mutation hook
  對所有寫入路徑強制執行）、純文字抽取、converter client。
- **MCP ingest**：`create_item`/`update_item` 的 richText 欄位可直接給 HTML 或
  Markdown 字串（agent 丟 Word/GDoc 轉出的 HTML 即可），nl-mcp 自動經 converter
  轉成 PM JSON；也可直接給 doc JSON 物件。`NL_RICHTEXT_URL` 指定 converter 位址。

## 網站閱讀用 HTML

`Post.contentHtml` 是由 `content` JSON 自動產生並儲存的語意化 HTML，
`renderVersion` 記錄產生器版本。網站可直接查詢這兩個唯讀欄位，讀取時不需轉換；
CMS／GraphQL／MCP 寫入 content 時會同步更新，清空內容也會清空 HTML。
網站配色、字體與間距由 CSS 控制，改樣式不需重產內容。

既有資料或 renderer 升版後執行 `./nl-server render rebuild`；
需要刷新所有圖片參照或現有輸出時執行 `./nl-server render rebuild --all`。
詳細 API、固定 class、媒體處理與部署流程見 [網站 HTML 契約](docs/content-html.md)。

## Admin UI

`http://localhost:8080/admin` —— server-rendered、schema-driven 的管理後台：
列表（cursor 分頁）、編輯表單（欄位依 DSL 型別生成）、新增與刪除。
**所有操作經 in-process GraphQL、帶登入者自己的 token**，權限與 API/MCP 完全一致，
admin 沒有特權路徑（editor 登入只看得到自己的文章、contributor 打 /admin/l/User
會被資料層擋下）。

- **Nocturne 介面**：登入、七種 lists 的列表與新增／編輯表單共用深色設計 tokens，
  桌面側欄與窄螢幕橫向導覽顯示登入者可見的資料筆數。
- 列表支援名稱／標題搜尋、Post 狀態／User 角色／Tag 精選篩選、更新時間排序與 cursor 分頁。
  批次操作可發佈／設為草稿、設定精選與刪除；每筆透過登入者的 GraphQL 權限驗證，
  部分失敗會列出結果。批次發佈僅為尚無 publishTime 的文章補上目前時間。
- 編輯頁提供固定儲存列、未儲存提醒、全螢幕富文字編輯及儲存失敗的內容保留。
  User 的密碼欄位直接更新現有密碼，留空不變更。
- 視覺樣式位於 `admin/static/nocturne.css`（design tokens／共用元件）與
  `admin/static/cms.css`（CMS layout／responsive）；互動來源為 `converter/admin-ui.mjs`。

- **richText = 內嵌 Tiptap 所見即所得編輯器**（粗體/斜體/標題/清單/引用/連結/YouTube）。
  編輯器 bundle 由 converter 以**同一份 schema**（[converter/schema.mjs](converter/schema.mjs)）
  建置，因此編輯器產物必然通過 server 端白名單驗證；改 schema 後執行
  `cd converter && npm run build` 重建 [admin/static/admin.js](admin/static/admin.js)（committed）。
- **關聯欄位 = 可搜尋 picker（chips）**：即時搜尋目標 list（`/admin/api/options`，
  label contains 不分大小寫、經 GraphQL 故權限一致），多值以 chips 增減。

## Migration

dev 模式啟動時自動套用（`NL_AUTO_MIGRATE=false` 可關閉）。正式環境兩條路：

```bash
# 快速路徑：declarative 兩段式
nl-server migrate plan                 # 印出「宣告 vs DB 現況」的 DDL（不執行）
nl-server migrate apply                # review 過後套用（非破壞性）

# 正式路徑：versioned migration（Atlas 引擎，golang-migrate 格式）
nl-server migrate diff <name>          # 產生 migrations/<ts>_<name>.up/.down.sql
                                       # （SQL 檔進 git、與 schema 變更同 PR review，
                                       #   可手動編輯：資料遷移、USING 轉型、CONCURRENTLY）
nl-server migrate up                   # 按序套用未執行的版本（記錄於 schema_migrations）
```

`migrate diff` 需要一個 scratch DB 重放既有 migrations（`NL_DEV_DATABASE_URL`，
預設 `nl_migrate_dev`，不存在會自動建立）。

## Deploy（GCP dev）

拓撲：三個無狀態 Cloud Run services + Cloud SQL。Admin UI 內建於 nl-server，
不是獨立 instance。

```
nl-server     GraphQL + /admin + OAuth AS + migration（唯一連 DB 的服務）
nl-mcp        MCP resource server（打 nl-server 的 GraphQL）
nl-converter  rich text 轉換（被 nl-mcp 呼叫）
Cloud SQL     PostgreSQL（唯一有狀態元件）
```

一次性前置作業：

```bash
# 1. Cloud SQL（PostgreSQL 16）+ database
gcloud sql instances create nl-dev --database-version=POSTGRES_16 --region=asia-east1 --tier=db-f1-micro
gcloud sql databases create nl --instance=nl-dev

# 2. Secrets（DATABASE_URL 用 Cloud SQL unix socket 形式）
echo -n "postgres://USER:PASS@/nl?host=/cloudsql/PROJECT:asia-east1:nl-dev&sslmode=disable" | \
  gcloud secrets create nl-database-url --data-file=-
openssl rand -hex 32 | tr -d '\n' | gcloud secrets create nl-session-secret --data-file=-

# 3. 首次 build & deploy（_CMS_URL/_RICHTEXT_URL 先用預設值）
gcloud builds submit --config cloudbuild.yaml \
  --substitutions=_CLOUDSQL_INSTANCE=PROJECT:asia-east1:nl-dev

# 4. 取得 nl-server / nl-converter 的實際 URL，回填 substitutions 再跑一次
#    （或設定 Cloud Build trigger 並在 trigger 上設定 substitutions）
gcloud run services list --region=asia-east1
```

dev 環境 nl-server 啟動時自動套用 migration（`NL_AUTO_MIGRATE=true`）；
prod 應改為 false 並在 release 流程執行 `nl-server migrate up`（versioned）。
dev 三個服務都 `--allow-unauthenticated`（nl-server/nl-mcp 本來就要公開給
OAuth 與 MCP clients）；prod 時 nl-converter 應改 service-to-service auth 或 internal ingress。

## 測試

```bash
createdb nl_test      # 一次性；或由測試自動建立
go test ./...
```

- [e2e/e2e_test.go](e2e/e2e_test.go)：對真實 Postgres 跑完整權限矩陣（三層 × 四角色），
  以及「MCP 操作與 GraphQL 直連權限一致」的驗證（in-memory MCP transport）。
- [nltest/](nltest/nltest.go)：測試 helper（連線、重置 schema、migration、權限掛載），
  框架使用者可重用來測自己的 schema。
- CI（[.github/workflows/ci.yml](.github/workflows/ci.yml)）：Postgres service container，
  驗證 codegen 無 drift + vet + build + 全部測試。

## 專案結構

```
lists/         ★ schema 宣告（唯一真相來源：欄位 + 關聯 + 權限）
nl/            DSL 型別與 registry；nl/field 欄位建構子
codegen/       nl gen 的生成邏輯（DSL → ent schema / graphqls / resolvers / owner helpers）
ent/schema/    生成的 ent schema（+ 手寫 TimeMixin、內部用 ApiKey）
ent/           ent codegen 產出的 typed client（committed）
graph/         GraphQL resolvers（生成的 CRUD + 手寫 auth）
access/        權限裁決引擎（ent interceptor/hook；規則執行期讀 DSL registry）
auth/          argon2id 密碼、HMAC session token、API key
server/        CMS HTTP handler 組裝（auth middleware、field-read middleware）
mcpserver/     MCP server 實作（tools、GraphQL client；metadata 執行期讀 DSL registry）
meta/          schema metadata 導出（MCP 探索用）
nltest/        整合測試 helper
e2e/           權限矩陣 + MCP 一致性整合測試
cmd/nl         框架 CLI（nl gen）
cmd/nl-server  CMS server 執行檔（migration + seed + serve）
cmd/nl-mcp     MCP server 執行檔（獨立部署，stdio / HTTP）
```

### 網站專用 GraphQL

`/graphql/content` 提供公開文章與分類的獨立唯讀 schema，使用專用 `content:read` API key。
由 admin/moderator 在 `/graphql` 呼叫 `createContentApiKey` 簽發，或 `revokeContentApiKey` 撤銷。
網站金鑰無法存取後台資料；單篇、列表與關聯均套用發布狀態及時間限制。

查詢範例、完整授權規則與部署步驟見 [網站內容 API](docs/content-api.md)。
