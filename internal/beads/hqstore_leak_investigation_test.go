package beads

import (
	"runtime"
	"testing"
)

func TestHQStoreOrderBoundedUnderChurn(t *testing.T) {
	store, err := OpenHQStore(t.TempDir(), WithHQStoreSnapshotInterval(0))
	if err != nil {
		t.Fatalf("OpenHQStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Shutdown(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	const liveCap = 100
	churnHQStore(t, store, liveCap, 50000)

	store.mu.RLock()
	liveBeads := len(store.main) + len(store.wisps)
	orderLen := len(store.order)
	orderSeenLen := len(store.orderSeen)
	store.mu.RUnlock()

	if orderLen > liveCap*3 || orderSeenLen > liveCap*3 {
		t.Fatalf("order not bounded under churn: live=%d order=%d orderSeen=%d",
			liveBeads, orderLen, orderSeenLen)
	}
}

func TestHQStoreHeapBoundedUnderChurn(t *testing.T) {
	store, err := OpenHQStore(t.TempDir(), WithHQStoreSnapshotInterval(0))
	if err != nil {
		t.Fatalf("OpenHQStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Shutdown(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	churnHQStore(t, store, 100, 200000)

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	var heapInuseDelta uint64
	if after.HeapInuse > before.HeapInuse {
		heapInuseDelta = after.HeapInuse - before.HeapInuse
	}
	const maxHeapInuseDelta = 20 << 20
	if heapInuseDelta > maxHeapInuseDelta {
		t.Fatalf("HeapInuse delta = %d bytes, want <= %d bytes", heapInuseDelta, maxHeapInuseDelta)
	}
}

func churnHQStore(t *testing.T, store *HQStore, liveCap, cycles int) {
	t.Helper()

	live := make([]string, 0, liveCap+1)
	for i := 0; i < cycles; i++ {
		b, err := store.Create(Bead{Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		live = append(live, b.ID)
		if len(live) <= liveCap {
			continue
		}
		if err := store.Delete(live[0]); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		live = live[1:]
	}
}
