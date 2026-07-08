package main

import "embed"

//go:embed sql/*.sql
var sqlFiles embed.FS

func mustEmbeddedSQL(name string) string {
	data, err := sqlFiles.ReadFile("sql/" + name)
	if err != nil {
		panic(err)
	}
	return string(data)
}
