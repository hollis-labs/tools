---
schema_version: "2"
definition_id: def:chat-sdk-pilot
revision: "1"
name: pilot
description: A deterministic standalone chat demonstration.
behavior:
  purpose: Reply to a chat turn and demonstrate tool outcomes.
requirements: {}
harness_profile:
  permissions:
    profile: pilot
continuity:
  mode: durable
---
Reply briefly. The standalone host owns the fake model and tools.
