---
schema_version: "2"
definition_id: fixture-operative
revision: "1"
name: fixture-operative
description: Synthetic CLI consumer with one in-memory tool
behavior:
  purpose: Exercise the actual SDK without external effects
requirements:
  requires: [approval]
  tools: [read_value]
harness_profile:
  permissions:
    profile: fixture
continuity:
  mode: ephemeral
---
Read the in-memory fixture value with read_value, then report the result.
