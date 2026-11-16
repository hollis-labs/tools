# localinvocation

`localinvocation` records a local command before it runs and links wrapped
children to their parent. It is a separate command tool in the `tools` repo;
Conduit remains a separate component.

The `invocation` package holds the record and file journal. The `entry` package
normalizes terminal argv, PATH shim argv, and provider exec-hook JSON into the
same record. The `runner` package executes commands and owns streams, PTY,
cancellation, reconnect and outcome evidence.

Each record keeps argv as a JSON array, an absolute cwd, a host-issued
environment reference, a session ID, a logical call ID and a distinct attempt
ID. Root and parent call IDs form the invocation tree. A retry uses the same
call ID and a new attempt ID. The journal never stores environment values.
The caller is responsible for giving the runner an environment resolver when
it supplies an environment reference.

The wrapper propagates only root call, parent call and session IDs through
`LOCALINVOCATION_*` variables. A child appears in the tree only when it is
itself wrapped. A provider hook that supplies a shell command string is
represented as `sh -c <script>`; the wrapper does not infer its leaf commands
from that string.

The default journal is a private JSONL file under the user's state directory.
An existing journal with group or world access is refused. A journal record is
preparation evidence, not proof of a command's effect; the runner's result is
the separate execution evidence.

## Entry points

```text
localinvocation run --cwd /workspace -- command "two words" ""
localinvocation hook --store /absolute/path/records.jsonl < hook-event.json
localinvocation-shim command "two words" ""
```

The shim can also be symlinked under a command name in `PATH`. It searches
later `PATH` entries for the real executable and skips itself, so it cannot
recurse. Its record names the resolved executable. `localinvocation hook`
accepts `argv` or a provider `tool_input.argv`; a `command` string is recorded
as a shell script. Hooks append a preparation record only. The standalone CLI
uses the current process environment; it refuses a nonempty environment
reference because it has no host resolver. Hosts can supply a resolver through
the runner API.

`LOCALINVOCATION_STORE` can select the journal path. The `run` entry point
propagates the chosen path to wrapped children. Runner output is kept under a
private sibling `runs/` directory and streamed to stdout and stderr by the
terminal entry point.

Run `go test -race ./...` from this directory for the tree, argv, entry,
concurrency and secret-exclusion fixtures.
