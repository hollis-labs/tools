# Wrapped invocation tree fixture

Run `go test -v ./examples/tree` from `localinvocation` to build the CLI and
shim into a temporary directory, execute a root shell with two shimmed child
commands, and verify the recorded tree. The test also runs timeout,
cancellation, lost-result, retry, and cursor-reconnect examples beneath that
root. `expected.json` is the stable, ID-free fixture asserted by the test.

Each final outcome is projected through `runner.Envelope` and validated against
the shared `agent/resultenv/v1` schema copied into `runner/testdata`. The lost
result is what a new manager observes while the original manager still owns an
unfinished attempt; the example waits for the original child before cleanup.
