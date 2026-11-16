package entry_test

import (
	"reflect"
	"testing"

	"github.com/hollis-labs/tools/localinvocation/entry"
)

func TestParseRunKeepsArgumentBoundaries(t *testing.T) {
	got, err := entry.ParseRun([]string{"--cwd", "/work", "--store", "/tmp/records", "--", "tool", "", "a b", "'quote'", "雪"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tool", "", "a b", "'quote'", "雪"}
	if !reflect.DeepEqual(got.Options.Argv, want) || got.Options.CWD != "/work" || got.Store != "/tmp/records" {
		t.Fatalf("parsed incorrectly: %+v", got)
	}
}

func TestParseRunRejectsMissingCommandAndRetryID(t *testing.T) {
	if _, err := entry.ParseRun([]string{"--store", "/tmp/records"}); err == nil {
		t.Fatal("missing command accepted")
	}
	settings, err := entry.ParseRun([]string{"--store", "/tmp/records", "--attempt", "2", "--", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := entry.Terminal(settings.Options.Argv, settings.Options); err == nil {
		t.Fatal("retry without call id accepted")
	}
}
