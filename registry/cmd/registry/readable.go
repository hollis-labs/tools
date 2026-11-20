package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// JSON stays the script contract; --readable is an explicit presentation mode.
func renderReadable(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	for _, field := range []string{"records", "revisions"} {
		if rows, ok := obj[field]; ok {
			var rs []struct {
				ID, Schema, Name, Publication string
				Revision                      string `json:"revision_id"`
				Lifecycle                     string
			}
			if err = json.Unmarshal(rows, &rs); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tSCHEMA\tNAME\tPUBLICATION\tLIFECYCLE\tREVISION")
			for _, r := range rs {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", cleanCell(r.ID), cleanCell(r.Schema), cleanCell(r.Name), cleanCell(r.Publication), cleanCell(r.Lifecycle), cleanCell(r.Revision))
			}
			if err = tw.Flush(); err != nil {
				return err
			}
			delete(obj, field)
			return renderFields(w, obj)
		}
	}
	return renderFields(w, obj)
}
func cleanCell(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}
func renderFields(w io.Writer, obj map[string]json.RawMessage) error {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var text string
		if json.Unmarshal(obj[k], &text) == nil {
			if _, err := fmt.Fprintf(w, "%s: %s\n", k, cleanCell(text)); err != nil {
				return err
			}
			continue
		}
		var v any
		if err := json.Unmarshal(obj[k], &v); err != nil {
			return err
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "%s: %s\n", k, b); err != nil {
			return err
		}
	}
	return nil
}
