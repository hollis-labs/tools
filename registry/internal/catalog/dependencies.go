package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/kinds"
)

const payloadEdgePrefix = "edge:payload:"

// Project descriptors already have a canonical edge representation. Other typed
// executable references become deterministic edges, while provenance, native
// bindings and descriptive references remain in the hashed authored payload.
func payloadEdges(r Record) ([]Edge, error) {
	edges := []Edge{}
	add := func(field string, kind kinds.EdgeType, ref kinds.Reference, pin string) {
		rel := kinds.Relation{Schema: kinds.RelationSchema, Type: kind, From: kinds.Reference{Authority: "registry", Kind: r.Kind, ID: r.ID}, To: ref, Source: "authored", Status: "proposed"}
		edges = append(edges, Edge{ID: payloadEdgePrefix + digest(struct{ ID, Field string }{r.ID, field}), Relation: rel, TargetRevision: pin})
	}
	many := func(field string, refs []kinds.Reference) {
		for i, ref := range refs {
			add(fmt.Sprintf("%s/%d", field, i), kinds.Consumes, ref, "")
		}
	}
	agent := func(prefix string, a kinds.AgentDefinition) {
		many(prefix+"skills", a.Skills)
		many(prefix+"tools", a.Tools)
		if a.LaunchProfile != nil {
			add(prefix+"launch_profile", kinds.Consumes, a.LaunchProfile.Reference, a.LaunchProfile.Revision)
		}
	}
	switch r.Kind {
	case "service", "config", "knowledge", "mcp_server", "launch_profile":
		refs, err := kinds.SemanticReferences(r.Schema, r.Payload)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			add("semantic/"+digest(ref), ref.Relation, ref.Reference, "")
		}
	case "skill":
		var v kinds.Skill
		if err := json.Unmarshal(r.Payload, &v); err != nil {
			return nil, err
		}
		many("tools", v.Tools)
		many("documents", v.Documents)
	case "agent":
		var v kinds.Agent
		if err := json.Unmarshal(r.Payload, &v); err != nil {
			return nil, err
		}
		agent("", v.AgentDefinition)
	case "agency":
		var v kinds.Agency
		if err := json.Unmarshal(r.Payload, &v); err != nil {
			return nil, err
		}
		for i, cell := range v.Cells {
			prefix := fmt.Sprintf("cells/%d/", i)
			if cell.Reference != nil {
				add(prefix+"reference", kinds.Consumes, *cell.Reference, "")
			}
			if cell.Definition != nil {
				agent(prefix, *cell.Definition)
			}
		}
	}
	return edges, nil
}

// Check the resulting published graph, substituting the entire candidate batch.
// A forbidden edge participates in a cycle iff its target can reach its source.
// Traversal uses pinned revisions, not mutable latest definitions.
func checkPublicationCycles(ctx context.Context, q store.Querier, records []Record) error {
	type node struct{ id, revision string }
	batch := map[node]Record{}
	for _, r := range records {
		batch[node{r.ID, r.RevisionID}] = r
	}
	loaded := map[node]Record{}
	limit := 10000
	var load func(node) (Record, error)
	load = func(n node) (Record, error) {
		if r, ok := batch[n]; ok {
			return r, nil
		}
		if r, ok := loaded[n]; ok {
			return r, nil
		}
		current, err := readPublished(ctx, q, n.id, -1)
		if err != nil || current.Lifecycle == "archived" || current.Lifecycle == "deleted" {
			return Record{}, failure("reference_unavailable", "edges", "transitive pinned dependency unavailable")
		}
		if len(loaded) >= limit {
			return Record{}, failure("validation_error", "edges", "publication graph exceeds validation bound")
		}
		var raw []byte
		err = q.QueryRowContext(ctx, `SELECT body FROM publication_events WHERE id=? AND revision=? ORDER BY seq DESC LIMIT 1`, n.id, n.revision).Scan(&raw)
		if err != nil {
			return Record{}, failure("reference_unavailable", "edges", "pinned revision unavailable")
		}
		var r Record
		if err = json.Unmarshal(raw, &r); err != nil {
			return r, err
		}
		loaded[n] = r
		return r, nil
	}
	neighbors := func(r Record) []node {
		out := []node{}
		for _, e := range r.Edges {
			if e.Relation.To.Authority == "registry" {
				pin := r.TargetPins[e.ID]
				if pin != "" {
					out = append(out, node{e.Relation.To.ID, pin})
				}
			}
		}
		return out
	}
	var reaches func(node, node, map[node]bool) (bool, error)
	reaches = func(from, to node, seen map[node]bool) (bool, error) {
		if from.id == to.id {
			return true, nil
		}
		if seen[from] {
			return false, nil
		}
		seen[from] = true
		r, err := load(from)
		if err != nil {
			return false, err
		}
		for _, next := range neighbors(r) {
			yes, err := reaches(next, to, seen)
			if err != nil || yes {
				return yes, err
			}
		}
		return false, nil
	}
	// Inspect every reachable edge, including preexisting pinned paths.
	queue := []node{}
	for n := range batch {
		queue = append(queue, n)
	}
	seen := map[node]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		r, err := load(n)
		if err != nil {
			return err
		}
		for _, e := range r.Edges {
			if e.Relation.To.Authority != "registry" {
				continue
			}
			pin := r.TargetPins[e.ID]
			if pin == "" {
				return failure("reference_unavailable", "edges", "target pin missing")
			}
			next := node{e.Relation.To.ID, pin}
			queue = append(queue, next)
			allowed, err := kinds.AllowsCycle(e.Relation.Type)
			if err != nil {
				return invalid("edges", err)
			}
			if !allowed {
				cycle, err := reaches(next, n, map[node]bool{})
				if err != nil {
					return err
				}
				if cycle {
					return failure("validation_error", "edges", "cycle contains a relation that forbids cycles")
				}
			}
		}
	}
	return nil
}

func isPayloadEdge(e Edge) bool { return strings.HasPrefix(e.ID, payloadEdgePrefix) }
