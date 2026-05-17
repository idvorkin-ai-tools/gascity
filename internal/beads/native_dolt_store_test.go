package beads

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	beadslib "github.com/steveyegge/beads"
)

func TestNativeDoltStoreCreateDelegatesToUpstreamStorage(t *testing.T) {
	createdAt := time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)
	priority := 1
	var captured *beadslib.Issue
	var capturedActor string
	storage := &nativeDoltStorageSpy{
		createIssue: func(_ context.Context, issue *beadslib.Issue, actor string) error {
			captured = cloneNativeIssueForTest(issue)
			capturedActor = actor
			issue.ID = "gc-native"
			issue.CreatedAt = createdAt
			issue.UpdatedAt = createdAt
			return nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	got, err := store.Create(Bead{
		Title:       "native create",
		Priority:    &priority,
		Description: "created through native store",
		Assignee:    "gascity/builder",
		Labels:      []string{"native", "dolt"},
		Metadata:    map[string]string{"gc.step_ref": "build"},
		Needs:       []string{"blocks:ga-parent"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if capturedActor == "" {
		t.Fatal("CreateIssue actor was empty")
	}
	if captured.Title != "native create" {
		t.Fatalf("upstream title = %q, want native create", captured.Title)
	}
	if captured.Status != beadslib.StatusOpen {
		t.Fatalf("upstream status = %q, want open", captured.Status)
	}
	if captured.IssueType != beadslib.TypeTask {
		t.Fatalf("upstream issue type = %q, want task", captured.IssueType)
	}
	if len(captured.Dependencies) != 1 || captured.Dependencies[0].DependsOnID != "ga-parent" || captured.Dependencies[0].Type != beadslib.DepBlocks {
		t.Fatalf("upstream dependencies = %#v, want blocks:ga-parent", captured.Dependencies)
	}
	if !json.Valid(captured.Metadata) {
		t.Fatalf("upstream metadata is invalid JSON: %q", captured.Metadata)
	}
	if got.ID != "gc-native" {
		t.Fatalf("created ID = %q, want gc-native", got.ID)
	}
	if got.Status != "open" {
		t.Fatalf("created status = %q, want open", got.Status)
	}
	if got.Type != "task" {
		t.Fatalf("created type = %q, want task", got.Type)
	}
	if got.Metadata["gc.step_ref"] != "build" {
		t.Fatalf("created metadata = %#v, want gc.step_ref=build", got.Metadata)
	}
}

func TestNativeDoltStoreCreatePropagatesUpstreamError(t *testing.T) {
	wantErr := errors.New("create failed")
	storage := &nativeDoltStorageSpy{
		createIssue: func(context.Context, *beadslib.Issue, string) error {
			return wantErr
		},
	}
	store := newNativeDoltStoreForTest(storage)

	if _, err := store.Create(Bead{Title: "native create"}); !errors.Is(err, wantErr) {
		t.Fatalf("Create error = %v, want %v", err, wantErr)
	}
}

func TestNativeDoltStoreGetPropagatesUpstreamError(t *testing.T) {
	wantErr := errors.New("get failed")
	storage := &nativeDoltStorageSpy{
		getIssue: func(context.Context, string) (*beadslib.Issue, error) {
			return nil, wantErr
		},
	}
	store := newNativeDoltStoreForTest(storage)

	if _, err := store.Get("gc-missing"); !errors.Is(err, wantErr) {
		t.Fatalf("Get error = %v, want %v", err, wantErr)
	}
}

func TestNativeDoltStoreListDelegatesAndConvertsIssues(t *testing.T) {
	createdAt := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	var capturedFilter beadslib.IssueFilter
	storage := &nativeDoltStorageSpy{
		searchIssues: func(_ context.Context, _ string, filter beadslib.IssueFilter) ([]*beadslib.Issue, error) {
			capturedFilter = filter
			return []*beadslib.Issue{{
				ID:          "gc-listed",
				Title:       "listed through native store",
				Status:      beadslib.StatusOpen,
				IssueType:   beadslib.TypeTask,
				Priority:    2,
				CreatedAt:   createdAt,
				Assignee:    "gascity/builder",
				Labels:      []string{"native"},
				Metadata:    json.RawMessage(`{"gc.step_ref":"list"}`),
				Description: "native list",
			}}, nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	got, err := store.List(ListQuery{AllowScan: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(capturedFilter.ExcludeStatus) != 1 || capturedFilter.ExcludeStatus[0] != beadslib.StatusClosed {
		t.Fatalf("ExcludeStatus = %#v, want [closed]", capturedFilter.ExcludeStatus)
	}
	if len(got) != 1 {
		t.Fatalf("List len = %d, want 1", len(got))
	}
	if got[0].ID != "gc-listed" || got[0].Title != "listed through native store" {
		t.Fatalf("listed bead = %#v, want converted upstream issue", got[0])
	}
	if got[0].Metadata["gc.step_ref"] != "list" {
		t.Fatalf("metadata = %#v, want gc.step_ref=list", got[0].Metadata)
	}
}

func TestNativeDoltStoreTxDelegatesToUpstreamTransaction(t *testing.T) {
	var capturedCommitMsg string
	var updatedID string
	var updated map[string]interface{}
	var metadataUpdate map[string]interface{}
	var closedID string
	tx := &nativeDoltTransactionSpy{
		getIssue: func(_ context.Context, id string) (*beadslib.Issue, error) {
			return &beadslib.Issue{
				ID:       id,
				Status:   beadslib.StatusOpen,
				Metadata: json.RawMessage(`{"keep":"yes"}`),
			}, nil
		},
		updateIssue: func(_ context.Context, id string, updates map[string]interface{}, _ string) error {
			if _, ok := updates["metadata"]; ok {
				metadataUpdate = updates
				return nil
			}
			updatedID = id
			updated = updates
			return nil
		},
		closeIssue: func(_ context.Context, id, _, _, _ string) error {
			closedID = id
			return nil
		},
	}
	storage := &nativeDoltStorageSpy{
		runInTransaction: func(_ context.Context, commitMsg string, fn func(beadslib.Transaction) error) error {
			capturedCommitMsg = commitMsg
			return fn(tx)
		},
	}
	store := newNativeDoltStoreForTest(storage)

	description := "updated in tx"
	err := store.Tx("native transaction", func(tx Tx) error {
		if err := tx.Update("gc-native", UpdateOpts{Description: &description}); err != nil {
			return err
		}
		if err := tx.SetMetadataBatch("gc-native", map[string]string{"gc.step_ref": "tx"}); err != nil {
			return err
		}
		return tx.Close("gc-native")
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	if capturedCommitMsg != "native transaction" {
		t.Fatalf("commitMsg = %q, want native transaction", capturedCommitMsg)
	}
	if updatedID != "gc-native" {
		t.Fatalf("updated ID = %q, want gc-native", updatedID)
	}
	if updated["description"] != "updated in tx" {
		t.Fatalf("updates = %#v, want description update", updated)
	}
	raw, ok := metadataUpdate["metadata"].(json.RawMessage)
	if !ok {
		t.Fatalf("metadata update = %#v, want json.RawMessage metadata", metadataUpdate)
	}
	var metadata map[string]string
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("metadata JSON: %v", err)
	}
	if metadata["keep"] != "yes" || metadata["gc.step_ref"] != "tx" {
		t.Fatalf("metadata = %#v, want merged metadata", metadata)
	}
	if closedID != "gc-native" {
		t.Fatalf("closed ID = %q, want gc-native", closedID)
	}
}

func TestNativeDoltStoreTxRetriesSerializationConflicts(t *testing.T) {
	origSleep := nativeTxSleep
	var slept []time.Duration
	nativeTxSleep = func(d time.Duration) {
		slept = append(slept, d)
	}
	t.Cleanup(func() { nativeTxSleep = origSleep })

	attempts := 0
	storage := &nativeDoltStorageSpy{
		runInTransaction: func(_ context.Context, _ string, fn func(beadslib.Transaction) error) error {
			attempts++
			if attempts < 3 {
				return nativeDoltSerializationConflictErrorForTest()
			}
			return fn(&nativeDoltTransactionSpy{})
		},
	}
	store := newNativeDoltStoreForTest(storage)

	called := 0
	if err := store.Tx("retry tx", func(Tx) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("Tx: %v", err)
	}

	if attempts != 3 {
		t.Fatalf("RunInTransaction attempts = %d, want 3", attempts)
	}
	if called != 1 {
		t.Fatalf("callback calls = %d, want 1 after conflicts clear", called)
	}
	if len(slept) != 2 || slept[0] != 50*time.Millisecond || slept[1] != 200*time.Millisecond {
		t.Fatalf("slept = %#v, want 50ms then 200ms", slept)
	}
}

func TestNativeDoltStoreTxStopsAfterMaxSerializationAttempts(t *testing.T) {
	origSleep := nativeTxSleep
	nativeTxSleep = func(time.Duration) {}
	t.Cleanup(func() { nativeTxSleep = origSleep })

	attempts := 0
	wantErr := nativeDoltSerializationConflictErrorForTest()
	storage := &nativeDoltStorageSpy{
		runInTransaction: func(context.Context, string, func(beadslib.Transaction) error) error {
			attempts++
			return wantErr
		},
	}
	store := newNativeDoltStoreForTest(storage)

	err := store.Tx("retry exhausted", func(Tx) error {
		t.Fatal("callback should not run when RunInTransaction fails before invoking it")
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Tx error = %v, want %v", err, wantErr)
	}
	if attempts != 3 {
		t.Fatalf("RunInTransaction attempts = %d, want 3", attempts)
	}
}

func TestNativeDoltStoreTxDoesNotRetryNonSerializationError(t *testing.T) {
	attempts := 0
	wantErr := errors.New("non-retry")
	storage := &nativeDoltStorageSpy{
		runInTransaction: func(context.Context, string, func(beadslib.Transaction) error) error {
			attempts++
			return wantErr
		},
	}
	store := newNativeDoltStoreForTest(storage)

	err := store.Tx("non-retry", func(Tx) error {
		t.Fatal("callback should not run when RunInTransaction returns an error")
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Tx error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("RunInTransaction attempts = %d, want 1", attempts)
	}
}

func TestNativeDoltStoreSetLocalStringDelegatesToUpstreamLocalMetadata(t *testing.T) {
	var capturedKey string
	var capturedValue string
	storage := &nativeDoltStorageSpy{
		setLocalMetadata: func(_ context.Context, key, value string) error {
			capturedKey = key
			capturedValue = value
			return nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	if err := store.SetLocalString("ga-dvvsla.2", "synced_at", "2026-05-17T21:30:00Z"); err != nil {
		t.Fatalf("SetLocalString: %v", err)
	}

	if capturedKey != "gc:bead:ga-dvvsla.2:synced_at" {
		t.Fatalf("local metadata key = %q, want gc:bead:ga-dvvsla.2:synced_at", capturedKey)
	}
	if capturedValue != "2026-05-17T21:30:00Z" {
		t.Fatalf("local metadata value = %q, want timestamp", capturedValue)
	}
}

func TestNativeDoltStoreSetLocalStringPropagatesUpstreamError(t *testing.T) {
	wantErr := errors.New("local metadata write failed")
	storage := &nativeDoltStorageSpy{
		setLocalMetadata: func(context.Context, string, string) error {
			return wantErr
		},
	}
	store := newNativeDoltStoreForTest(storage)

	if err := store.SetLocalString("ga-dvvsla.2", "synced_at", "value"); !errors.Is(err, wantErr) {
		t.Fatalf("SetLocalString error = %v, want %v", err, wantErr)
	}
}

func TestNativeDoltStoreGetLocalStringDelegatesToUpstreamLocalMetadata(t *testing.T) {
	var capturedKey string
	storage := &nativeDoltStorageSpy{
		getLocalMetadata: func(_ context.Context, key string) (string, error) {
			capturedKey = key
			return "2026-05-17T21:30:00Z", nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	value, ok, err := store.GetLocalString("ga-dvvsla.2", "last_woke_at")
	if err != nil {
		t.Fatalf("GetLocalString: %v", err)
	}

	if capturedKey != "gc:bead:ga-dvvsla.2:last_woke_at" {
		t.Fatalf("local metadata key = %q, want gc:bead:ga-dvvsla.2:last_woke_at", capturedKey)
	}
	if !ok {
		t.Fatal("GetLocalString ok = false, want true")
	}
	if value != "2026-05-17T21:30:00Z" {
		t.Fatalf("GetLocalString value = %q, want timestamp", value)
	}
}

func TestNativeDoltStoreGetLocalStringTranslatesAbsentLocalMetadata(t *testing.T) {
	storage := &nativeDoltStorageSpy{
		getLocalMetadata: func(context.Context, string) (string, error) {
			return "", nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	value, ok, err := store.GetLocalString("ga-dvvsla.2", "pending_create_claim")
	if err != nil {
		t.Fatalf("GetLocalString: %v", err)
	}
	if ok {
		t.Fatal("GetLocalString ok = true, want false")
	}
	if value != "" {
		t.Fatalf("GetLocalString value = %q, want empty", value)
	}
}

func TestNativeDoltStoreGetLocalStringPropagatesUpstreamError(t *testing.T) {
	wantErr := errors.New("local metadata read failed")
	storage := &nativeDoltStorageSpy{
		getLocalMetadata: func(context.Context, string) (string, error) {
			return "", wantErr
		},
	}
	store := newNativeDoltStoreForTest(storage)

	_, _, err := store.GetLocalString("ga-dvvsla.2", "synced_at")
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetLocalString error = %v, want %v", err, wantErr)
	}
}

func TestNativeDoltStoreLocalMetadataNamespaceAvoidsBeadslibKeys(t *testing.T) {
	got := localMetadataKey("ga-dvvsla.2", "linear.last_sync")
	if got != "gc:bead:ga-dvvsla.2:linear.last_sync" {
		t.Fatalf("localMetadataKey = %q, want gc:bead namespace with bead ID", got)
	}
	for _, upstreamKey := range []string{
		"bd_version",
		"bd_version_max",
		"linear.last_sync",
		"test.last_sync",
	} {
		if got == upstreamKey {
			t.Fatalf("localMetadataKey collided with upstream beadslib key %q", upstreamKey)
		}
	}
}

func TestNativeDoltStoreConfigGetDelegatesToUpstreamStorage(t *testing.T) {
	var capturedKey string
	storage := &nativeDoltStorageSpy{
		getConfig: func(_ context.Context, key string) (string, error) {
			capturedKey = key
			return "v1", nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	got, err := store.ConfigGet("gc:local-metadata-migrated")
	if err != nil {
		t.Fatalf("ConfigGet: %v", err)
	}
	if capturedKey != "gc:local-metadata-migrated" {
		t.Fatalf("config key = %q, want marker key", capturedKey)
	}
	if got != "v1" {
		t.Fatalf("config value = %q, want v1", got)
	}
}

func TestNativeDoltStoreConfigSetDelegatesToUpstreamStorage(t *testing.T) {
	var capturedKey, capturedValue string
	storage := &nativeDoltStorageSpy{
		setConfig: func(_ context.Context, key, value string) error {
			capturedKey = key
			capturedValue = value
			return nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	if err := store.ConfigSet("gc:local-metadata-migrated", "v1"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}
	if capturedKey != "gc:local-metadata-migrated" {
		t.Fatalf("config key = %q, want marker key", capturedKey)
	}
	if capturedValue != "v1" {
		t.Fatalf("config value = %q, want v1", capturedValue)
	}
}

type nativeDoltStorageSpy struct {
	beadslib.Storage
	createIssue      func(context.Context, *beadslib.Issue, string) error
	getIssue         func(context.Context, string) (*beadslib.Issue, error)
	searchIssues     func(context.Context, string, beadslib.IssueFilter) ([]*beadslib.Issue, error)
	runInTransaction func(context.Context, string, func(beadslib.Transaction) error) error
	setLocalMetadata func(context.Context, string, string) error
	getLocalMetadata func(context.Context, string) (string, error)
	getConfig        func(context.Context, string) (string, error)
	setConfig        func(context.Context, string, string) error
}

func (s *nativeDoltStorageSpy) CreateIssue(ctx context.Context, issue *beadslib.Issue, actor string) error {
	return s.createIssue(ctx, issue, actor)
}

func (s *nativeDoltStorageSpy) GetIssue(ctx context.Context, id string) (*beadslib.Issue, error) {
	return s.getIssue(ctx, id)
}

func (s *nativeDoltStorageSpy) SearchIssues(ctx context.Context, query string, filter beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	return s.searchIssues(ctx, query, filter)
}

func (s *nativeDoltStorageSpy) RunInTransaction(ctx context.Context, commitMsg string, fn func(beadslib.Transaction) error) error {
	return s.runInTransaction(ctx, commitMsg, fn)
}

func (s *nativeDoltStorageSpy) SetLocalMetadata(ctx context.Context, key, value string) error {
	return s.setLocalMetadata(ctx, key, value)
}

func (s *nativeDoltStorageSpy) GetLocalMetadata(ctx context.Context, key string) (string, error) {
	return s.getLocalMetadata(ctx, key)
}

func (s *nativeDoltStorageSpy) GetConfig(ctx context.Context, key string) (string, error) {
	return s.getConfig(ctx, key)
}

func (s *nativeDoltStorageSpy) SetConfig(ctx context.Context, key, value string) error {
	return s.setConfig(ctx, key, value)
}

type nativeDoltTransactionSpy struct {
	beadslib.Transaction
	getIssue      func(context.Context, string) (*beadslib.Issue, error)
	updateIssue   func(context.Context, string, map[string]interface{}, string) error
	closeIssue    func(context.Context, string, string, string, string) error
	addLabel      func(context.Context, string, string, string) error
	removeLabel   func(context.Context, string, string, string) error
	addDependency func(context.Context, *beadslib.Dependency, string) error
	removeDep     func(context.Context, string, string, string) error
	depRecords    func(context.Context, string) ([]*beadslib.Dependency, error)
}

func (t *nativeDoltTransactionSpy) GetIssue(ctx context.Context, id string) (*beadslib.Issue, error) {
	return t.getIssue(ctx, id)
}

func (t *nativeDoltTransactionSpy) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	return t.updateIssue(ctx, id, updates, actor)
}

func (t *nativeDoltTransactionSpy) CloseIssue(ctx context.Context, id string, reason string, actor string, session string) error {
	return t.closeIssue(ctx, id, reason, actor, session)
}

func (t *nativeDoltTransactionSpy) AddLabel(ctx context.Context, issueID, label, actor string) error {
	return t.addLabel(ctx, issueID, label, actor)
}

func (t *nativeDoltTransactionSpy) RemoveLabel(ctx context.Context, issueID, label, actor string) error {
	return t.removeLabel(ctx, issueID, label, actor)
}

func (t *nativeDoltTransactionSpy) AddDependency(ctx context.Context, dep *beadslib.Dependency, actor string) error {
	return t.addDependency(ctx, dep, actor)
}

func (t *nativeDoltTransactionSpy) RemoveDependency(ctx context.Context, issueID, dependsOnID, actor string) error {
	return t.removeDep(ctx, issueID, dependsOnID, actor)
}

func (t *nativeDoltTransactionSpy) GetDependencyRecords(ctx context.Context, issueID string) ([]*beadslib.Dependency, error) {
	return t.depRecords(ctx, issueID)
}

func cloneNativeIssueForTest(issue *beadslib.Issue) *beadslib.Issue {
	cloned := *issue
	cloned.Metadata = append(json.RawMessage(nil), issue.Metadata...)
	cloned.Labels = append([]string(nil), issue.Labels...)
	cloned.Dependencies = append([]*beadslib.Dependency(nil), issue.Dependencies...)
	return &cloned
}

func nativeDoltSerializationConflictErrorForTest() error {
	return &mysql.MySQLError{Number: 1213, Message: "deadlock found when trying to get lock"}
}
