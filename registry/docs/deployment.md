# Deployment and activation plan

Status: prepared for review; no deployment, registration, population or consumer
migration has been performed by this change. The driver owns merges and the
activation checkpoint. Do not treat this runbook as permission to restart a
shared gateway.

1. Merge the reviewed Registry revision and wait for its root build workflow.
   Use the resulting Linux amd64 artifact and checksums, carrying deploy.yaml,
   project.yaml and registry.cerberus.yaml. The reusable build is pinned by the
   generated workflow; no tag is required for a class 2 artifact.
2. Recheck the target account/platform, binary-name availability and private
   listener availability. The checked-in fleet host is a placeholder; prepare
   operator-local descriptors for the actual inventory host. Resolve config,
   database, secret/env-file and installed paths absolutely. Keep the private
   topology out of this public repository.
3. Provision the operator-owned Substrate verifier and narrow publication/review
   bindings through existing protected configuration. No raw credential enters
   the descriptor or logs. Confirm an unprivileged read credential cannot write
   or review, and a writer cannot grant itself elevated review authority.
4. Back up an existing SQLite database with the SQLite backup API or a stopped
   service; copying only a live WAL database file is insufficient. A first
   install creates an empty store, with no population/import work. Config stays
   under XDG_CONFIG_HOME, canonical records under XDG_DATA_HOME/registry, secret
   state under XDG_STATE_HOME/registry.
5. Run `fleet-install deploy registry --commit MERGED_SHA --dry-run` against the
   verified artifact/target. Review the concrete result before activation.
   Fleet-install owns `~/.local/bin/registry` and versioned releases; Cerberus
   supervises that installed link (Option B), with no source build/artifact path.
   Only the Registry resource is registered/applied at this checkpoint.
6. Use the current Cerberus workflow to validate/register the resolved manifest
   and supervise `registry serve --managed --addr RESOLVED_PRIVATE_ADDRESS`.
   Honor fleet-install's pending-restart exit 10 and `--restart-done` contract;
   do not mark deployment complete just because an old process is healthy.
7. Prove the running executable matches the intended artifact SHA/version and
   that public health plus authenticated readiness/diagnostics pass. Verify
   create/get/update/search, restart persistence, body preservation and scoped
   denial through API/CLI/direct MCP using clearly marked synthetic records.
   Archive/tombstone those test records afterward; no bulk data population.
8. Prepare the supported Tether HTTP upstream registration pointing at `/mcp`,
   with protected token-file reference and only intended client grants. Current
   onboarding guidance says a new upstream can remain pending_restart until a
   daemon reload. Present the exact affected gateway, consumers, reload/recovery
   steps and timing to the driver. No shared Tether reload, protected opt-in
   expansion or active workstream restart belongs to this PR's implementation.
9. At the separately coordinated gateway activation checkpoint, prove tool
   discovery and an actual consumer call. Direct MCP success alone is not
   gateway acceptance. Record the installed version, running SHA, canonical
   database path, resolved endpoint and remaining limitations in the task log.

Recovery: stop/restart only Registry through Cerberus. Fleet-install can switch
back to a verified previous binary, but binary rollback does not undo a database
migration. Preserve the failed database and restore a consistent pre-migration
backup only after assessing intervening writes. Disable/remove only the Registry
upstream registration if gateway verification fails; coordinate any shared
reload needed for rollback. Do not touch runtime session/lease authorities.

`project.yaml` is the authored descriptor. Folio's deployment projection can
render local manifests after runtime settings change; inspect `folio deploy
render --plan` before `--write`. The platform/build target must match the artifact.
The generated descriptor/runtime checks remain separate from shared structural
project parsing.
