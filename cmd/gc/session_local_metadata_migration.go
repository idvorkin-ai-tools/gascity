package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

const (
	localLifecycleMetadataMigrationMarkerKey   = "gc:local-metadata-migrated"
	localLifecycleMetadataMigrationMarkerValue = "v1"
)

type localLifecycleMetadataConfigStore interface {
	ConfigGet(key string) (string, error)
	ConfigSet(key, value string) error
}

type localLifecycleMetadataBackingStore interface {
	Backing() beads.Store
}

func migrateLocalLifecycleMetadataOnce(store beads.Store, stderr io.Writer) error {
	if store == nil {
		return nil
	}
	if stderr == nil {
		stderr = io.Discard
	}
	configStore, ok := localLifecycleMetadataConfigStoreFor(store)
	if !ok {
		return nil
	}
	value, err := configStore.ConfigGet(localLifecycleMetadataMigrationMarkerKey)
	if err != nil {
		return fmt.Errorf("reading local metadata migration marker: %w", err)
	}
	if strings.TrimSpace(value) == localLifecycleMetadataMigrationMarkerValue {
		return nil
	}

	sessions, err := store.List(beads.ListQuery{Label: sessionBeadLabel})
	if err != nil {
		return fmt.Errorf("listing session beads for local metadata migration: %w", err)
	}
	for _, session := range sessions {
		if session.Status == "closed" || !sessionpkg.IsSessionBeadOrRepairable(session) {
			continue
		}
		if err := migrateLocalLifecycleMetadataForBead(store, session); err != nil {
			fmt.Fprintf(stderr, "local metadata migration: bead %s: %v\n", session.ID, err) //nolint:errcheck
		}
	}
	if err := configStore.ConfigSet(localLifecycleMetadataMigrationMarkerKey, localLifecycleMetadataMigrationMarkerValue); err != nil {
		return fmt.Errorf("writing local metadata migration marker: %w", err)
	}
	return nil
}

func localLifecycleMetadataConfigStoreFor(store beads.Store) (localLifecycleMetadataConfigStore, bool) {
	if store == nil {
		return nil, false
	}
	if configStore, ok := store.(localLifecycleMetadataConfigStore); ok {
		return configStore, true
	}
	if backed, ok := store.(localLifecycleMetadataBackingStore); ok {
		return localLifecycleMetadataConfigStoreFor(backed.Backing())
	}
	return nil, false
}

func migrateLocalLifecycleMetadataForBead(store beads.Store, bead beads.Bead) error {
	if bead.Metadata == nil {
		return nil
	}
	clearBatch := make(map[string]string, len(localLifecycleMetadataKeys))
	var joined error
	for _, key := range localLifecycleMetadataKeys {
		value, ok := bead.Metadata[key]
		if !ok || value == "" {
			continue
		}
		if err := store.SetLocalString(bead.ID, key, value); err != nil {
			joined = errors.Join(joined, fmt.Errorf("setting local %s: %w", key, err))
			continue
		}
		clearBatch[key] = ""
	}
	if len(clearBatch) == 0 {
		return joined
	}
	if err := store.Tx(fmt.Sprintf("migrate local lifecycle metadata for %s", bead.ID), func(tx beads.Tx) error {
		return tx.SetMetadataBatch(bead.ID, clearBatch)
	}); err != nil {
		joined = errors.Join(joined, fmt.Errorf("clearing durable lifecycle metadata: %w", err))
	}
	return joined
}
