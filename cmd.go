package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type command struct {
	name    string
	usage   string
	summary string
}

type helpJSONCommand struct {
	Name    string `json:"name"`
	Usage   string `json:"usage"`
	Summary string `json:"summary"`
}

type helpJSONResult struct {
	Usage    string            `json:"usage"`
	Commands []helpJSONCommand `json:"commands"`
}

var commands = []command{
	{name: "help", usage: "code-index help [--format text|json] [COMMAND]", summary: "show command help"},
	{name: "version", usage: "code-index version [--format text|json]", summary: "show build and schema information"},
	{name: "path", usage: "code-index path [--format text|json] ROOT", summary: "print the default database path for a root"},
	{name: "init", usage: "code-index init [--db DB] [--format text|json] ROOT", summary: "initialize an empty index database"},
	{name: "rebuild", usage: "code-index rebuild [--db DB] [--max-bytes N] [--format text|json] ROOT", summary: "atomically rebuild the full index"},
	{name: "update", usage: "code-index update [--db DB] [--max-bytes N] [--adopt] [--format text|json] ROOT", summary: "create or incrementally refresh the index"},
	{name: "defs", usage: "code-index defs [--root ROOT|--db DB] [--kind KIND] [--language LANG] [--list] [--format text|json] [QUERY]", summary: "list or find symbol definitions"},
	{name: "files", usage: "code-index files [--root ROOT|--db DB] [--language LANG] [--list] [--format text|json] [QUERY]", summary: "list or find indexed files"},
	{name: "sql", usage: "code-index sql [--root ROOT|--db DB] [--format text|json] [SQL]", summary: "run read-only SQL"},
	{name: "show", usage: "code-index show [--root ROOT|--db DB] --line N [--context N] [--format text|json] PATH", summary: "show indexed source around a line"},
	{name: "schema", usage: "code-index schema [--root ROOT|--db DB] [--format text|json]", summary: "show index tables and columns"},
	{name: "stats", usage: "code-index stats [--root ROOT|--db DB] [--format text|json]", summary: "show index table counts"},
	{name: "metrics", usage: "code-index metrics [--root ROOT|--db DB] [--language LANG] [--limit N] [--format text|json] [PATH_QUERY]", summary: "show indexed code metrics"},
	{name: "status", usage: "code-index status [--root ROOT|--db DB] [--format text|json]", summary: "show index metadata, lock state, and freshness"},
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return errors.New("missing command")
	}
	if handler := commandHandler(args[0]); handler != nil {
		return handler(args[1:])
	}
	printUsage(os.Stderr)
	return fmt.Errorf("unknown command: %s", args[0])
}

func commandHandler(name string) func([]string) error {
	switch name {
	case "help":
		return cmdHelp
	case "version":
		return cmdVersion
	case "path":
		return cmdPath
	case "init":
		return cmdInit
	case "rebuild":
		return cmdRebuild
	case "update":
		return cmdUpdate
	case "defs":
		return cmdDefs
	case "files":
		return cmdFiles
	case "sql":
		return cmdSQL
	case "show":
		return cmdShow
	case "schema":
		return cmdSchema
	case "stats":
		return cmdStats
	case "metrics":
		return cmdMetrics
	case "status":
		return cmdStatus
	default:
		return nil
	}
}

func usage() {
	printUsage(os.Stderr)
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, "usage: %s\n", topLevelUsage())
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, cmd := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", cmd.name, cmd.summary)
	}
}

func topLevelUsage() string {
	names := make([]string, 0, len(commands))
	for _, cmd := range commands {
		names = append(names, cmd.name)
	}
	return fmt.Sprintf("code-index <%s> [options]", strings.Join(names, "|"))
}

func commandUsage(name string) string {
	for _, cmd := range commands {
		if cmd.name == name {
			return "usage: " + cmd.usage
		}
	}
	return "usage: code-index " + name
}

func cmdHelp(args []string) error {
	fs := flag.NewFlagSet("help", flag.ExitOnError)
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New(commandUsage("help"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		if format == outputFormatJSON {
			result := helpJSONResult{Usage: topLevelUsage(), Commands: make([]helpJSONCommand, 0, len(commands))}
			for _, cmd := range commands {
				result.Commands = append(result.Commands, helpJSONCommand{Name: cmd.name, Usage: cmd.usage, Summary: cmd.summary})
			}
			return writeJSON(os.Stdout, result)
		}
		printUsage(os.Stdout)
		return nil
	}
	for _, cmd := range commands {
		if cmd.name == fs.Arg(0) {
			if format == outputFormatJSON {
				return writeJSON(os.Stdout, helpJSONCommand{Name: cmd.name, Usage: cmd.usage, Summary: cmd.summary})
			}
			fmt.Println("usage: " + cmd.usage)
			fmt.Println()
			fmt.Println(cmd.summary)
			return nil
		}
	}
	return fmt.Errorf("unknown command: %s", fs.Arg(0))
}

func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("version"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	info := currentBuildInfo()
	if format == outputFormatJSON {
		schema, err := strconv.ParseInt(schemaVersion, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid schema version %q: %w", schemaVersion, err)
		}
		result := versionJSONResult{
			Commit:        versionCommitPointer(info.commit),
			Modified:      versionModifiedPointer(info.modified),
			SchemaVersion: schema,
			FileSource:    fileSource,
		}
		return writeJSON(os.Stdout, result)
	}
	fmt.Println("key\tvalue")
	fmt.Printf("commit\t%s\n", info.commit)
	if info.modified != "" {
		fmt.Printf("modified\t%s\n", info.modified)
	}
	fmt.Printf("schema_version\t%s\n", schemaVersion)
	fmt.Printf("file_source\t%s\n", fileSource)
	return nil
}

func cmdPath(args []string) error {
	fs := flag.NewFlagSet("path", flag.ExitOnError)
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(commandUsage("path"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	path := defaultDBPath(root)
	if format == outputFormatJSON {
		return writeJSON(os.Stdout, struct {
			Path string `json:"path"`
		}{Path: path})
	}
	fmt.Println(path)
	return nil
}
