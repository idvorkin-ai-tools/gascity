package graphv2

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/formulatest"
)

func TestPrepareInvocationCreatesInputConvoyForBeadTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy == "" {
		t.Fatalf("invocation = %+v, want input convoy", inv)
	}
	if got := inv.Vars[ConvoyIDVar]; got != inv.InputConvoy {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, inv.InputConvoy)
	}
	members, err := convoycore.Members(store, inv.InputConvoy, true)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].ID != target.ID {
		t.Fatalf("members = %+v, want target %s", members, target.ID)
	}
	created, err := store.Get(inv.InputConvoy)
	if err != nil {
		t.Fatalf("Get(input convoy): %v", err)
	}
	if created.Type != "convoy" {
		t.Fatalf("input convoy type = %q, want convoy", created.Type)
	}
	if got := created.Metadata["gc.synthetic"]; got != "true" {
		t.Fatalf("input convoy gc.synthetic = %q, want true", got)
	}
}

func TestPrepareInvocationUsesExistingConvoyTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	convoy, err := store.Create(beads.Bead{Title: "input", Type: "convoy"})
	if err != nil {
		t.Fatalf("Create convoy: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, convoy.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy != convoy.ID {
		t.Fatalf("invocation = %+v, want existing convoy", inv)
	}
}

func TestPrepareInvocationRejectsCallerReservedVars(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, map[string]string{"issue": target.ID})
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want reserved var error")
	}
	if !strings.Contains(err.Error(), "reserved variable") {
		t.Fatalf("error = %q, want reserved variable", err)
	}
}

func TestPrepareInvocationTargetlessRejectsConvoyReference(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)

	_, err := PrepareInvocation(context.Background(), beads.NewMemStore(), "work", []string{dir}, "", nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want targetless convoy_id error")
	}
	if !strings.Contains(err.Error(), "convoy_id requires a targeted graph.v2 invocation") {
		t.Fatalf("error = %q, want convoy_id target error", err)
	}
}

func TestPreparePreviewInvocationUsesPreviewInputConvoyForBeadTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PreparePreviewInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PreparePreviewInvocation: %v", err)
	}
	want := previewInputConvoyPrefix + target.ID
	if inv.InputConvoy != want {
		t.Fatalf("preview invocation = %+v, want preview input convoy %q", inv, want)
	}
	if got := inv.Vars[ConvoyIDVar]; got != want {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, want)
	}
	matches, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List convoys: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("preview created input convoys = %+v, want none", matches)
	}
}

func writeFormula(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write formula %s: %v", path, err)
	}
}
