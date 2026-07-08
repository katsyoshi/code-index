package main

import (
	"embed"
	"fmt"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

func mustEmbeddedSQL(name string) string {
	data, err := sqlFiles.ReadFile("sql/" + name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func formatEmbeddedSQL(name string, args ...any) string {
	return fmt.Sprintf(mustEmbeddedSQL(name), args...)
}
