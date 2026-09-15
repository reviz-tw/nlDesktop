// nl：框架 CLI。
//
//	nl gen    從 lists 宣告展開所有 artifacts 並執行 ent/gqlgen codegen
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/hcchien/nl/codegen"

	// 註冊 lists 宣告
	_ "github.com/hcchien/nl/lists"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "gen" {
		fmt.Fprintln(os.Stderr, "usage: nl gen")
		os.Exit(2)
	}
	if err := codegen.Generate("."); err != nil {
		log.Fatalf("nl gen: %v", err)
	}
	log.Println("nl gen: DSL artifacts written")
	for _, cmd := range [][]string{
		{"go", "generate", "./ent"},
		{"go", "tool", "gqlgen", "generate"},
		{"go", "tool", "gqlgen", "generate", "--config", "contentgraph/gqlgen.yml"},
	} {
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			log.Fatalf("nl gen: running %v: %v", cmd, err)
		}
		log.Printf("nl gen: %v done", cmd)
	}
}
