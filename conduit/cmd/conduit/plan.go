package main

import (
	"slices"
	"strings"

	"github.com/hollis-labs/tools/conduit/internal/persistence"
)

// A command line starts only the modules it needs, and only the commands that must write may create anything. Even
// `conduit --help` must not boot the framework: that creates a signing key and a database in the data directory, and
// on a host with a live installation it fills the live directory. See plan.
type runPlan struct {
	modules []string
	// prepare creates the data directory (0700). False for everything that only reads.
	prepare bool
	// open is what the database may be asked to do; ignored when the plan has no database module.
	open persistence.Open
	// app is true when the command needs the service built over the started framework.
	app bool
	// noStore is true for the commands that are pure functions of the policy and the parser: the service is built
	// without a database.
	noStore bool
}

var (
	// helpModules start nothing that opens or creates a file.
	helpModules = []string{"config", "logging", "health", "cli"}
	// dataModules are what the local commands need: the database (which must already exist, or may be created for the
	// commands that write). No listener, no identity (the signing key is not made here).
	dataModules = []string{"config", "logging", "secrets", "database", "cli"}
)

// stateless commands never touch the database; writers may create it; every other command word that is a command of
// the tree only reads an existing one.
var (
	stateless = []string{"check", "parse", "runbooks", "init", "paths", "approve", "reject"}
	writers   = []string{"run", "cancel", "reap"}
)

// plan decides, from the command line, which modules to start and what may be created. Only `serve`, `migrate-legacy`,
// `identity setup` and `identity key create` start the full set; `serve` and the identity commands may create the
// database, the signing key and the directory. Help at every level, version, config, health, completion and an
// unknown word start no module that opens a file.
//
// Every argument that does not start with "-" counts as a command word. That is right while the root command has no
// global flag that takes a value; TestRootHasNoValueFlags fails if one is added, and plan must then learn it.
func plan(args []string, commands []string) runPlan {
	help := runPlan{modules: pick(helpModules)}
	var words []string
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "--version" || a == "help" && len(words) == 0 {
			return help
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		return help
	}
	full := func(open persistence.Open, prepare bool) runPlan {
		return runPlan{modules: substrateModules, prepare: prepare, open: open, app: true}
	}
	switch {
	case words[0] == "serve":
		return full(persistence.Create, true)
	case words[0] == "mcp":
		return full(persistence.ReadExisting, false)
	case words[0] == "migrate-legacy":
		return full(persistence.CreateForMigration, true)
	case words[0] == "identity" && len(words) > 1 && (words[1] == "setup" || (words[1] == "key" && len(words) > 2 && words[2] == "create")):
		return runPlan{modules: substrateModules, prepare: true, open: persistence.Create}
	case slices.Contains(stateless, words[0]):
		return runPlan{modules: pick(helpModules), app: true, noStore: true}
	case slices.Contains(writers, words[0]):
		return runPlan{modules: pick(dataModules), prepare: true, open: persistence.Create, app: true}
	case slices.Contains(commands, words[0]):
		return runPlan{modules: pick(dataModules), open: persistence.ReadExisting, app: true}
	}
	return help
}

// pick keeps the start order of substrateModules.
func pick(want []string) []string {
	var out []string
	for _, m := range substrateModules {
		if slices.Contains(want, m) {
			out = append(out, m)
		}
	}
	return out
}
