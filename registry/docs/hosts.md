# Host inventory (`host/1`)

A host record describes current inventory. It does not configure a machine,
launch services, certify health, or grant operational authority. Keep history,
observations and credentials in their owning systems. Use maintained source
pointers rather than copying service definitions, configuration or runbooks.

The payload `id` is a lowercase DNS hostname (short or fully qualified, up to
253 characters); the record key must equal it. Canonical identity is
`host:<hostname>`. No case folding or hostname alias resolution is performed.
Required fields are `schema`, `id`, `name`, `roles`, `os`, `tailnet`, `services`,
`owner`, `operator` and `project`. `summary`, `config` and `runbook` are optional.
Unknown properties, including runtime/history fields, are rejected.

- `roles` is a nonempty unique list of `hub`, `worker`, `build`, `util`, `lab`,
  or `laptop`. A machine may have multiple roles.
- `os` is a nonempty description of the current operating system.
- `tailnet` contains `hostname`, `address`, or both. The hostname follows the
  same DNS syntax as `id`; the address is an IPv4 or IPv6 literal without a zone,
  CIDR prefix, port or URL. It is a discovery address, never an enrollment key.
- `services` is an explicit list (possibly empty) of typed service references.
  `config` is a list of typed config references; `runbook` is a document
  reference. All retain authority, kind and native ID, with an optional URI.
- `owner` names the responsible owner. `operator` is a typed reference to the
  operating entity; `project` is a typed project reference. They generate
  `operated_by` and `part_of` edges respectively. Services, config and runbook
  pointers are descriptive and generate no dependency edges. Local Registry
  targets follow the normal existence/unresolved-reference and publication rules.

Illustrative payload (documentation-only host and address):

```json
{
  "schema": "host/1",
  "id": "example-host",
  "name": "Example build host",
  "roles": ["worker", "build"],
  "os": "Example Linux",
  "tailnet": {"hostname": "host.example.test", "address": "192.0.2.10"},
  "services": [{"authority": "registry", "kind": "service", "id": "service:example"}],
  "owner": "user:example",
  "operator": {"authority": "registry", "kind": "agency", "id": "agency:fleet"},
  "project": {"authority": "registry", "kind": "project", "id": "project:example"},
  "config": [{"authority": "git", "kind": "config", "id": "inventory/host"}],
  "runbook": {"authority": "git", "kind": "document", "id": "docs/host.md"}
}
```

`Schema`, `Schemas`, `ValidateJSON`, `ParseContentJSON`, `Host.Validate`,
`CanonicalContentID` and `SemanticReferences` support this version. Publish
inventory through the normal Registry mutation/publication APIs; schema
registration alone neither seeds inventory nor activates consumers.
