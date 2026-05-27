//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestIntegrationBboltBackendBeadLifecycleSurvivesRestart(t *testing.T) {
	cityName := "bbolt-e2e-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	cityDir := setupE2ECity(t, nil, e2eCity{
		Workspace: e2eWorkspace{Name: cityName},
		Beads:     e2eBeads{Provider: "bd", Backend: "bbolt"},
	})
	baseURL := e2eSupervisorBaseURL(t, cityDir)
	cityBase := "/v0/city/" + url.PathEscape(cityName)
	waitHTTP(t, baseURL+cityBase+"/health", 15*time.Second)

	assertNoManagedDoltSQLServerForBboltCity(t, cityDir)
	bboltPath := filepath.Join(cityDir, ".gc", "state", "bbolt", "beads.bolt")
	if _, err := os.Stat(bboltPath); err != nil {
		t.Fatalf("bbolt store file %s: %v", bboltPath, err)
	}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	created := liveContractJSON[beads.Bead](t, baseURL, nil, http.MethodPost, cityBase+"/beads", map[string]any{
		"description": "Bbolt E2E lifecycle fixture created by integration test.",
		"labels":      []string{"bbolt-e2e", "ready-to-build"},
		"metadata": map[string]string{
			"bbolt_e2e.run_id": runID,
		},
		"priority": 1,
		"title":    "bbolt E2E lifecycle " + runID,
		"type":     "task",
	}, http.StatusCreated)
	if created.ID == "" || created.Status != "open" {
		t.Fatalf("created bead = %+v, want id and open status", created)
	}
	if created.Metadata["bbolt_e2e.run_id"] != runID {
		t.Fatalf("created metadata = %#v, want run_id=%q", created.Metadata, runID)
	}

	liveContractJSON[struct {
		Status string `json:"status"`
	}](t, baseURL, nil, http.MethodPost, cityBase+"/bead/"+url.PathEscape(created.ID)+"/update", map[string]any{
		"labels":        []string{"claimed"},
		"metadata":      map[string]string{"bbolt_e2e.updated": "true"},
		"remove_labels": []string{"ready-to-build"},
		"status":        "in_progress",
	}, http.StatusOK)
	updated := liveContractJSON[beads.Bead](t, baseURL, nil, http.MethodGet, cityBase+"/bead/"+url.PathEscape(created.ID), nil, http.StatusOK)
	if updated.Status != "in_progress" {
		t.Fatalf("updated bead = %+v, want in_progress status", updated)
	}
	if updated.Metadata["bbolt_e2e.updated"] != "true" || !containsString(updated.Labels, "claimed") || containsString(updated.Labels, "ready-to-build") {
		t.Fatalf("updated bead labels/metadata = labels=%#v metadata=%#v", updated.Labels, updated.Metadata)
	}

	liveContractJSON[struct {
		Status string `json:"status"`
	}](t, baseURL, nil, http.MethodPost, cityBase+"/bead/"+url.PathEscape(created.ID)+"/close", nil, http.StatusOK)
	closed := liveContractJSON[beads.Bead](t, baseURL, nil, http.MethodGet, cityBase+"/bead/"+url.PathEscape(created.ID), nil, http.StatusOK)
	if closed.Status != "closed" {
		t.Fatalf("closed bead status = %q, want closed", closed.Status)
	}

	out, err := gc("", "stop", cityDir)
	if err != nil {
		t.Fatalf("gc stop failed: %v\noutput: %s", err, out)
	}
	out, err = gc("", "start", cityDir)
	if err != nil {
		t.Fatalf("gc start after stop failed: %v\noutput: %s", err, out)
	}
	waitForControllerReady(t, cityDir, 15*time.Second)
	waitHTTP(t, baseURL+cityBase+"/health", 15*time.Second)

	afterRestart := liveContractJSON[beads.Bead](t, baseURL, nil, http.MethodGet, cityBase+"/bead/"+url.PathEscape(created.ID), nil, http.StatusOK)
	if afterRestart.Status != "closed" || afterRestart.Metadata["bbolt_e2e.updated"] != "true" {
		t.Fatalf("bead after restart = %+v, want closed bead with metadata", afterRestart)
	}
	assertNoManagedDoltSQLServerForBboltCity(t, cityDir)
}

func e2eSupervisorBaseURL(t *testing.T, cityDir string) string {
	t.Helper()
	env := parseEnvList(commandEnvForDir(cityDir, false))
	gcHome := env["GC_HOME"]
	if gcHome == "" {
		t.Fatal("isolated command env missing GC_HOME")
	}
	return "http://127.0.0.1:" + readSupervisorPortFromConfig(t, gcHome)
}

func assertNoManagedDoltSQLServerForBboltCity(t *testing.T, cityDir string) {
	t.Helper()
	procs := readProcessSnapshot()
	if len(procs) == 0 {
		t.Log("process snapshot unavailable; skipping managed Dolt SQL server assertion")
		return
	}

	roots := []string{cityDir}
	env := parseEnvList(commandEnvForDir(cityDir, false))
	if gcHome := env["GC_HOME"]; gcHome != "" {
		roots = append(roots, filepath.Dir(gcHome))
	}
	for _, root := range roots {
		pids := integrationDoltSQLServerKillSet(procs, root)
		if len(pids) == 0 {
			continue
		}
		t.Fatalf("found managed Dolt SQL server process under %s:\n%s", root, describeProcessSet(procs, pids))
	}
}

func describeProcessSet(procs map[int]procSnapshot, pids map[int]bool) string {
	details := make([]string, 0, len(pids))
	for pid := range pids {
		details = append(details, strconv.Itoa(pid)+": "+procs[pid].cmd)
	}
	slices.Sort(details)
	return strings.Join(details, "\n")
}
