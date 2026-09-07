// notes keeps a list in the agent's own database. It is the demonstration that
// a tool may own tables: a db_prefix, a schema applied on load, and a panel in
// the interface reading them back through the API.
package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"agent/internal/tool"
)

type args struct {
	Action string `json:"action"`
	Text   string `json:"text"`
}

func main() {
	var a args
	tool.Args(&a)
	if tool.DBPath == "" {
		tool.Failf("AGENT_DB is not set, so there is nowhere to keep a note")
	}
	db, err := sql.Open("sqlite", tool.DBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		tool.Failf("cannot open the database: %v", err)
	}
	defer db.Close()

	if a.Action == "add" {
		text := strings.TrimSpace(a.Text)
		if text == "" {
			tool.Failf("text is required")
		}
		if _, err := db.Exec("insert into notes_items(text, session) values(?,?)",
			text, tool.Session); err != nil {
			tool.Failf("cannot write the note: %v", err)
		}
		tool.OK("noted")
	}

	rows, err := db.Query("select id, text, created_at from notes_items order by id desc limit 50")
	if err != nil {
		tool.Failf("cannot read the notes: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id int
		var text, created string
		if err := rows.Scan(&id, &text, &created); err != nil {
			tool.Failf("cannot read the notes: %v", err)
		}
		lines = append(lines, fmt.Sprintf("%d. %s  (%s)", id, text, created))
	}
	if err := rows.Err(); err != nil {
		tool.Failf("cannot read the notes: %v", err)
	}
	if len(lines) == 0 {
		tool.OK("no notes")
	}
	tool.OK(strings.Join(lines, "\n"))
}
