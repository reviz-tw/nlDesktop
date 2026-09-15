// nl-server：CMS 主程式。GraphQL API + auth + migration。
//
// 用法：
//
//	nl-server                 啟動 server（dev：自動套用 migration）
//	nl-server seed            建立範例使用者與資料
//	nl-server migrate plan    印出待套用的 DDL（供 review，不執行）
//	nl-server migrate apply   套用 migration（非破壞性；drop 需明確 flag）
//
// 環境變數：
//
//	DATABASE_URL       預設 postgres://localhost:5432/nl_dev?sslmode=disable
//	PORT               預設 8080
//	NL_SESSION_SECRET  session token 簽章密鑰（正式環境必設）
//	NL_AUTO_MIGRATE    設為 false 時啟動不自動 migration（prod 建議，配合 migrate plan/apply）
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/postrender"
	"github.com/hcchien/nl/server"

	// 註冊 lists 宣告（access/meta 於執行期從 nl registry 導出）
	_ "github.com/hcchien/nl/lists"
)

func main() {
	dbURL := envOr("DATABASE_URL", "postgres://localhost:5432/nl_dev?sslmode=disable")
	port := envOr("PORT", "8080")
	secret := []byte(envOr("NL_SESSION_SECRET", "dev-secret-change-me"))

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	ctx := context.Background()

	if len(os.Args) > 2 && os.Args[1] == "migrate" {
		switch os.Args[2] {
		case "plan":
			// 印出「宣告 schema vs DB 現況」的 DDL diff，不執行
			if err := client.Schema.WriteTo(ctx, os.Stdout); err != nil {
				log.Fatalf("migrate plan: %v", err)
			}
		case "apply":
			if err := client.Schema.Create(ctx); err != nil {
				log.Fatalf("migrate apply: %v", err)
			}
			log.Println("migrate apply: done")
		case "diff":
			// 產生版本化 migration 檔：nl-server migrate diff <name>
			if len(os.Args) < 4 {
				log.Fatal("usage: nl-server migrate diff <name>")
			}
			if err := migrateDiff(ctx, os.Args[3]); err != nil {
				log.Fatalf("migrate diff: %v", err)
			}
			log.Printf("migrate diff: files written to %s/", migrationsDir)
		case "up":
			if err := migrateUp(dbURL); err != nil {
				log.Fatalf("migrate up: %v", err)
			}
		default:
			log.Fatalf("usage: nl-server migrate plan|apply|diff <name>|up")
		}
		return
	}

	if envOr("NL_AUTO_MIGRATE", "true") == "true" {
		if err := server.Setup(ctx, client); err != nil {
			log.Fatalf("running schema migration: %v", err)
		}
		log.Println("schema migration applied")
	} else {
		server.Attach(client)
		log.Println("auto-migrate disabled (NL_AUTO_MIGRATE=false); use `nl-server migrate plan|apply`")
	}

	if len(os.Args) > 1 && os.Args[1] == "render" {
		if len(os.Args) < 3 || os.Args[2] != "rebuild" || len(os.Args) > 4 || (len(os.Args) == 4 && os.Args[3] != "--all") {
			log.Fatal("usage: nl-server render rebuild [--all]")
		}
		result, err := postrender.Rebuild(auth.WithSystem(ctx), client, len(os.Args) == 4)
		if err != nil {
			log.Fatalf("HTML rebuild failed after %d updates: %v", result.Updated, err)
		}
		log.Printf("HTML rebuild: %d updated, %d skipped concurrent edits", result.Updated, result.Skipped)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "seed" {
		if err := seed(auth.WithSystem(ctx), client); err != nil {
			log.Fatalf("seeding: %v", err)
		}
		return
	}

	log.Printf("nl-server listening on http://localhost:%s (playground on /)", port)
	if err := http.ListenAndServe(":"+port, server.New(client, secret)); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
