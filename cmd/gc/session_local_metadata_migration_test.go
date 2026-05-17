package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

type localMetadataMigrationStore struct {
	beads.Store

	config map[string]string
	local  map[string]map[string]string

	listErr      error
	configGetErr error
	configSetErr error
	setLocalErr  map[string]error
	txErr        map[string]error

	getConfigCalls int
	setConfigCalls int
	listCalls      int
	txCalls        int
	events         []string
}

func newLocalMetadataMigrationStore() *localMetadataMigrationStore {
	return &localMetadataMigrationStore{
		Store:       beads.NewMemStore(),
		config:      make(map[string]string),
		local:       make(map[string]map[string]string),
		setLocalErr: make(map[string]error),
		txErr:       make(map[string]error),
	}
}

func (s *localMetadataMigrationStore) ConfigGet(key string) (string, error) {
	s.getConfigCalls++
	if s.configGetErr != nil {
		return "", s.configGetErr
	}
	return s.config[key], nil
}

func (s *localMetadataMigrationStore) ConfigSet(key, value string) error {
	s.setConfigCalls++
	if s.configSetErr != nil {
		return s.configSetErr
	}
	s.config[key] = value
	return nil
}

func (s *localMetadataMigrationStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.List(query)
}

func (s *localMetadataMigrationStore) SetLocalString(beadID, key, value string) error {
	s.events = append(s.events, fmt.Sprintf("local:%s:%s", beadID, key))
	if err := s.setLocalErr[beadID+":"+key]; err != nil {
		return err
	}
	if s.local[beadID] == nil {
		s.local[beadID] = make(map[string]string)
	}
	s.local[beadID][key] = value
	return nil
}

func (s *localMetadataMigrationStore) GetLocalString(beadID, key string) (string, bool, error) {
	if s.local[beadID] == nil {
		return "", false, nil
	}
	value, ok := s.local[beadID][key]
	return value, ok, nil
}

func (s *localMetadataMigrationStore) Tx(_ string, fn func(beads.Tx) error) error {
	s.txCalls++
	return fn(localMetadataMigrationTx{s: s})
}

type localMetadataMigrationTx struct {
	s *localMetadataMigrationStore
}

func (tx localMetadataMigrationTx) Update(id string, opts beads.UpdateOpts) error {
	return tx.s.Update(id, opts)
}

func (tx localMetadataMigrationTx) SetMetadataBatch(id string, kvs map[string]string) error {
	tx.s.events = append(tx.s.events, "tx:"+id)
	if err := tx.s.txErr[id]; err != nil {
		return err
	}
	return tx.s.SetMetadataBatch(id, kvs)
}

func (tx localMetadataMigrationTx) Close(id string) error {
	return tx.s.Close(id)
}

func createMigrationSessionBead(t *testing.T, store beads.Store, metadata map[string]string) beads.Bead {
	t.Helper()
	bead, err := store.Create(beads.Bead{
		Title:    "session",
		Type:     sessionBeadType,
		Labels:   []string{sessionBeadLabel},
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("Create session bead: %v", err)
	}
	return bead
}

func TestMigrateLocalLifecycleMetadataCopiesClearsAndSetsMarker(t *testing.T) {
	store := newLocalMetadataMigrationStore()
	session := createMigrationSessionBead(t, store, map[string]string{
		"session_name":          "worker",
		"synced_at":             "2026-05-17T22:00:00Z",
		"last_woke_at":          "",
		"pending_create_claim":  "true",
		"durable_unrelated_key": "keep",
	})
	_, _ = store.Create(beads.Bead{
		Title:    "not a session",
		Metadata: map[string]string{"synced_at": "should-stay-durable"},
	})

	var stderr bytes.Buffer
	if err := migrateLocalLifecycleMetadataOnce(store, &stderr); err != nil {
		t.Fatalf("migrateLocalLifecycleMetadataOnce: %v", err)
	}

	if got := store.local[session.ID]["synced_at"]; got != "2026-05-17T22:00:00Z" {
		t.Fatalf("local synced_at = %q, want timestamp", got)
	}
	if got := store.local[session.ID]["pending_create_claim"]; got != "true" {
		t.Fatalf("local pending_create_claim = %q, want true", got)
	}
	if _, ok := store.local[session.ID]["last_woke_at"]; ok {
		t.Fatal("empty last_woke_at should not be copied to local metadata")
	}

	updated, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get session bead: %v", err)
	}
	if updated.Metadata["synced_at"] != "" {
		t.Fatalf("durable synced_at = %q, want cleared", updated.Metadata["synced_at"])
	}
	if updated.Metadata["pending_create_claim"] != "" {
		t.Fatalf("durable pending_create_claim = %q, want cleared", updated.Metadata["pending_create_claim"])
	}
	if updated.Metadata["durable_unrelated_key"] != "keep" {
		t.Fatalf("unrelated durable key = %q, want keep", updated.Metadata["durable_unrelated_key"])
	}
	if got := store.config[localLifecycleMetadataMigrationMarkerKey]; got != localLifecycleMetadataMigrationMarkerValue {
		t.Fatalf("migration marker = %q, want %q", got, localLifecycleMetadataMigrationMarkerValue)
	}
	if want := []string{"local:" + session.ID + ":synced_at", "local:" + session.ID + ":pending_create_claim", "tx:" + session.ID}; strings.Join(store.events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", store.events, want)
	}
}

func TestMigrateLocalLifecycleMetadataAlreadyMarkedIsO1(t *testing.T) {
	store := newLocalMetadataMigrationStore()
	store.config[localLifecycleMetadataMigrationMarkerKey] = localLifecycleMetadataMigrationMarkerValue
	createMigrationSessionBead(t, store, map[string]string{"synced_at": "2026-05-17T22:00:00Z"})

	if err := migrateLocalLifecycleMetadataOnce(store, io.Discard); err != nil {
		t.Fatalf("migrateLocalLifecycleMetadataOnce: %v", err)
	}
	if store.getConfigCalls != 1 {
		t.Fatalf("ConfigGet calls = %d, want 1", store.getConfigCalls)
	}
	if store.listCalls != 0 {
		t.Fatalf("List calls = %d, want 0", store.listCalls)
	}
	if store.setConfigCalls != 0 {
		t.Fatalf("ConfigSet calls = %d, want 0", store.setConfigCalls)
	}
}

func TestMigrateLocalLifecycleMetadataPerBeadErrorContinuesAndSetsMarker(t *testing.T) {
	store := newLocalMetadataMigrationStore()
	failing := createMigrationSessionBead(t, store, map[string]string{"synced_at": "2026-05-17T22:00:00Z"})
	success := createMigrationSessionBead(t, store, map[string]string{"pending_create_claim": "true"})
	store.txErr[failing.ID] = errors.New("clear failed")

	var stderr bytes.Buffer
	if err := migrateLocalLifecycleMetadataOnce(store, &stderr); err != nil {
		t.Fatalf("migrateLocalLifecycleMetadataOnce: %v", err)
	}

	if !strings.Contains(stderr.String(), failing.ID) || !strings.Contains(stderr.String(), "clear failed") {
		t.Fatalf("stderr = %q, want bead context and clear failure", stderr.String())
	}
	if got := store.local[success.ID]["pending_create_claim"]; got != "true" {
		t.Fatalf("successful bead local pending_create_claim = %q, want true", got)
	}
	successAfter, err := store.Get(success.ID)
	if err != nil {
		t.Fatalf("Get success bead: %v", err)
	}
	if successAfter.Metadata["pending_create_claim"] != "" {
		t.Fatalf("successful bead durable pending_create_claim = %q, want cleared", successAfter.Metadata["pending_create_claim"])
	}
	failingAfter, err := store.Get(failing.ID)
	if err != nil {
		t.Fatalf("Get failing bead: %v", err)
	}
	if failingAfter.Metadata["synced_at"] == "" {
		t.Fatal("failing bead durable synced_at was cleared despite Tx failure")
	}
	if got := store.config[localLifecycleMetadataMigrationMarkerKey]; got != localLifecycleMetadataMigrationMarkerValue {
		t.Fatalf("migration marker = %q, want %q", got, localLifecycleMetadataMigrationMarkerValue)
	}
}

func TestMigrateLocalLifecycleMetadataListFailureDoesNotWriteMarker(t *testing.T) {
	store := newLocalMetadataMigrationStore()
	store.listErr = errors.New("list failed")

	if err := migrateLocalLifecycleMetadataOnce(store, io.Discard); err == nil {
		t.Fatal("migrateLocalLifecycleMetadataOnce error = nil, want list failure")
	}
	if got := store.config[localLifecycleMetadataMigrationMarkerKey]; got != "" {
		t.Fatalf("migration marker = %q, want empty", got)
	}
}

func TestMigrateLocalLifecycleMetadataSkipsStoresWithoutMarkerCapability(t *testing.T) {
	store := beads.NewMemStore()
	session := createMigrationSessionBead(t, store, map[string]string{"synced_at": "2026-05-17T22:00:00Z"})

	if err := migrateLocalLifecycleMetadataOnce(store, io.Discard); err != nil {
		t.Fatalf("migrateLocalLifecycleMetadataOnce: %v", err)
	}
	updated, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get session bead: %v", err)
	}
	if got := updated.Metadata["synced_at"]; got != "2026-05-17T22:00:00Z" {
		t.Fatalf("durable synced_at = %q, want unchanged", got)
	}
}

func TestCityRuntimeMigratesLocalLifecycleMetadataUsingCityStore(t *testing.T) {
	store := newLocalMetadataMigrationStore()
	old := cityRuntimeMigrateLocalLifecycleMetadata
	t.Cleanup(func() { cityRuntimeMigrateLocalLifecycleMetadata = old })

	var calledWith beads.Store
	cityRuntimeMigrateLocalLifecycleMetadata = func(store beads.Store, _ io.Writer) error {
		calledWith = store
		return errors.New("migration failed")
	}

	var stderr bytes.Buffer
	cr := &CityRuntime{
		standaloneCityStore: store,
		logPrefix:           "gc start",
		stderr:              &stderr,
	}
	cr.migrateLocalLifecycleMetadata()

	if calledWith != store {
		t.Fatalf("migration called with %T, want city store", calledWith)
	}
	if !strings.Contains(stderr.String(), "local metadata migration") || !strings.Contains(stderr.String(), "migration failed") {
		t.Fatalf("stderr = %q, want migration warning", stderr.String())
	}
}
