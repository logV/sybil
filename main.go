package main

import cmd "github.com/logv/sybil/src/cmd"

import "fmt"
import "os"
import "log"
import "sort"

var CMD_FUNCS = make(map[string]func())
var CMD_KEYS = make([]string, 0)

func setupCommands() {
	CMD_FUNCS["ingest"] = cmd.RunIngestCmdLine
	CMD_FUNCS["digest"] = cmd.RunDigestCmdLine
	CMD_FUNCS["trim"] = cmd.RunTrimCmdLine
	CMD_FUNCS["query"] = cmd.RunQueryCmdLine
	CMD_FUNCS["index"] = cmd.RunIndexCmdLine
	CMD_FUNCS["rebuild"] = cmd.RunRebuildCmdLine
	CMD_FUNCS["inspect"] = cmd.RunInspectCmdLine
	CMD_FUNCS["aggregate"] = cmd.RunAggregateCmdLine
	CMD_FUNCS["version"] = cmd.RunVersionCmdLine

	for k, _ := range CMD_FUNCS {
		CMD_KEYS = append(CMD_KEYS, k)
	}
}

var USAGE = `sybil: a fast and simple NoSQL column store

Commands: ingest, digest, trim, query, index, rebuild, inspect, aggregate, version, serve

Storage Commands:

  ingest: ingest records into the row store

    example: sybil ingest -table TABLE < my_record.json
    example: sybil ingest -table TABLE -csv < my_records.csv

  digest: collate row store records into column blocks

    example: sybil digest -table TABLE

  trim: trim a table to fit into a set amount of space or time limit

    example: sybil trim -table TABLE -mb 100 -list
    example: sybil trim -table TABLE -mb 100 -delete

Query Commands:

  query: run aggregation queries on records inside a table

    example: sybil query -table TABLE -info
    example: sybil query -table TABLE -print -group col1 -int col2 -op hist
    # reads the row store log (off by default)
    example: sybil query -table TABLE -read-log -print -group col1 -int col2 -op hist

Emergency Maintenance Commands:

  rebuild: re-create the main table info.db based on the consensus of blocks' info.db

    example: sybil rebuild -table TABLE

  inspect: examine sybil .db files

    example: sybil inspect -file ./db/TABLE/info.db
    example: sybil inspect -file ./db/TABLE/BLOCK/info.db
    example: sybil inspect -file ./db/TABLE/BLOCK/str_COL.db

`

func printCommandHelp(msg string) {
	sort.Strings(CMD_KEYS)

	fmt.Print(USAGE)
	log.Fatal(msg)
}

func main() {
	setupCommands()

	if len(os.Args) < 2 {
		printCommandHelp("insufficient number of arguments")
	}

	first_arg := os.Args[1]
	os.Args = os.Args[1:]

	handler, ok := CMD_FUNCS[first_arg]
	if !ok {
		printCommandHelp(fmt.Sprintf("subcommand '%s' is invalid", first_arg))
	}

	handler()

}
