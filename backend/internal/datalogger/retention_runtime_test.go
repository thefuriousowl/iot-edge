package datalogger

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewRetentionRuntimeValidatesDependenciesAndOptions(t *testing.T) {
	t.Parallel()
	repository := newRetentionRuntimeTestRepository()
	cleaner := newRetentionRuntimeTestCleaner()
	tests := []struct {
		name       string
		repository RetentionRuntimeRepository
		cleaner    RetentionCleaner
		options    []RetentionRuntimeOption
		want       error
	}{
		{name: "repository", cleaner: cleaner, want: ErrRetentionRuntimeRepositoryRequired},
		{name: "cleaner", repository: repository, want: ErrRetentionRuntimeCleanerRequired},
		{name: "negative interval", repository: repository, cleaner: cleaner, options: []RetentionRuntimeOption{WithRetentionRuntimeInterval(-time.Second)}, want: ErrInvalidInput},
		{name: "zero batch limit", repository: repository, cleaner: cleaner, options: []RetentionRuntimeOption{WithRetentionRuntimeBatchLimit(0)}, want: ErrInvalidInput},
		{name: "large batch limit", repository: repository, cleaner: cleaner, options: []RetentionRuntimeOption{WithRetentionRuntimeBatchLimit(MaxRetentionBatchLimit + 1)}, want: ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRetentionRuntime(test.repository, test.cleaner, test.options...); !errors.Is(err, test.want) {
				t.Fatalf("NewRetentionRuntime() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRetentionRuntimeStartupPassIsBoundedAndIsolatesLoggerFailures(t *testing.T) {
	t.Parallel()
	firstID, failedID, lastID := uuid.New(), uuid.New(), uuid.New()
	repository := newRetentionRuntimeTestRepository(firstID, uuid.Nil, failedID, lastID)
	cleaner := newRetentionRuntimeTestCleaner()
	cleanupError := errors.New("cleanup unavailable")
	cleaner.errors[failedID] = []error{cleanupError}
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	runtime := newRetentionRuntimeTest(t, repository, cleaner, 0, 7)
	runtime.now = func() time.Time { return evaluatedAt }
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)
	cleaner.awaitCallCount(t, lastID, 1)
	for _, loggerID := range []uuid.UUID{firstID, failedID, lastID} {
		input := cleaner.lastInput(loggerID)
		if input.BatchLimit != 7 || !input.EvaluatedAt.Equal(evaluatedAt.UTC()) {
			t.Errorf("cleanup input %s = %#v", loggerID, input)
		}
	}
	seenInvalid, seenFailure := false, false
	deadline := time.After(time.Second)
	for !seenInvalid || !seenFailure {
		select {
		case runtimeError := <-runtime.Errors():
			seenInvalid = seenInvalid || errors.Is(runtimeError, ErrInvalidInput)
			seenFailure = seenFailure || errors.Is(runtimeError, cleanupError) && strings.Contains(runtimeError.Error(), failedID.String())
		case <-deadline:
			t.Fatalf("runtime errors invalid/failure = %t/%t", seenInvalid, seenFailure)
		}
	}
}

func TestRetentionRuntimePeriodicallyRetriesFailuresAndIncompleteWork(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	repository := newRetentionRuntimeTestRepository(loggerID)
	cleaner := newRetentionRuntimeTestCleaner()
	retryError := errors.New("temporary cleanup failure")
	cleaner.errors[loggerID] = []error{retryError, nil}
	runtime := newRetentionRuntimeTest(t, repository, cleaner, 5*time.Millisecond, 1)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	cleaner.awaitCallCount(t, loggerID, 2)
	select {
	case runtimeError := <-runtime.Errors():
		if !errors.Is(runtimeError, retryError) {
			t.Fatalf("runtime error = %v", runtimeError)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not report retryable cleanup failure")
	}
	runtime.Stop()
	stoppedCalls := cleaner.callCount(loggerID)
	time.Sleep(20 * time.Millisecond)
	if cleaner.callCount(loggerID) != stoppedCalls {
		t.Errorf("cleanup calls after Stop = %d, want %d", cleaner.callCount(loggerID), stoppedCalls)
	}
}

func TestRetentionRuntimeReportsRepositoryOutageAndRecovers(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	repository := newRetentionRuntimeTestRepository()
	repository.setError(errors.New("database unavailable"))
	cleaner := newRetentionRuntimeTestCleaner()
	runtime := newRetentionRuntimeTest(t, repository, cleaner, 5*time.Millisecond, DefaultRetentionBatchLimit)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)
	select {
	case runtimeError := <-runtime.Errors():
		if !strings.Contains(runtimeError.Error(), "reconciling Data Logger retention") || !strings.Contains(runtimeError.Error(), "database unavailable") {
			t.Fatalf("runtime error = %v", runtimeError)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not report repository outage")
	}
	repository.setLoggerIDs(loggerID)
	repository.setError(nil)
	cleaner.awaitCallCount(t, loggerID, 1)
}

func TestRetentionRuntimeRunsStartupReconciliationAfterRestart(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	repository := newRetentionRuntimeTestRepository(loggerID)
	cleaner := newRetentionRuntimeTestCleaner()
	for run := 1; run <= 2; run++ {
		runtime := newRetentionRuntimeTest(t, repository, cleaner, 0, DefaultRetentionBatchLimit)
		if err := runtime.Start(context.Background()); err != nil {
			t.Fatalf("Start(run %d) error = %v", run, err)
		}
		cleaner.awaitCallCount(t, loggerID, run)
		runtime.Stop()
	}
}

func TestRetentionRuntimeLifecycleCancelsCleanupCleanly(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	repository := newRetentionRuntimeTestRepository(loggerID)
	cleaner := newRetentionRuntimeTestCleaner()
	entered := make(chan struct{})
	cleaner.run = func(ctx context.Context, _ uuid.UUID, _ RetentionCleanupInput) (*RetentionCleanupResult, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	runtime := newRetentionRuntimeTest(t, repository, cleaner, 0, DefaultRetentionBatchLimit)
	if err := runtime.Start(nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Start(nil) error = %v", err)
	}
	if err := runtime.Reconcile(context.Background()); !errors.Is(err, ErrRetentionRuntimeNotStarted) {
		t.Fatalf("Reconcile(before Start) error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-entered
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRetentionRuntimeAlreadyStarted) {
		t.Fatalf("Start(second) error = %v", err)
	}
	stopped := make(chan struct{})
	go func() {
		runtime.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not wait for canceled cleanup")
	}
	runtime.Stop()
	if err := runtime.Reconcile(context.Background()); !errors.Is(err, ErrRetentionRuntimeNotStarted) {
		t.Fatalf("Reconcile(after Stop) error = %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRetentionRuntimeAlreadyStarted) {
		t.Fatalf("Start(after Stop) error = %v", err)
	}
}

type retentionRuntimeTestRepository struct {
	mu     sync.Mutex
	ids    []uuid.UUID
	err    error
	called chan struct{}
}

func newRetentionRuntimeTestRepository(ids ...uuid.UUID) *retentionRuntimeTestRepository {
	return &retentionRuntimeTestRepository{ids: append([]uuid.UUID(nil), ids...), called: make(chan struct{}, 32)}
}

func (repository *retentionRuntimeTestRepository) ListRetentionLoggerIDs(context.Context) ([]uuid.UUID, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.called <- struct{}{}
	return append([]uuid.UUID(nil), repository.ids...), repository.err
}

func (repository *retentionRuntimeTestRepository) setLoggerIDs(ids ...uuid.UUID) {
	repository.mu.Lock()
	repository.ids = append([]uuid.UUID(nil), ids...)
	repository.mu.Unlock()
}

func (repository *retentionRuntimeTestRepository) setError(err error) {
	repository.mu.Lock()
	repository.err = err
	repository.mu.Unlock()
}

type retentionRuntimeTestCleaner struct {
	mu     sync.Mutex
	calls  map[uuid.UUID][]RetentionCleanupInput
	errors map[uuid.UUID][]error
	called chan uuid.UUID
	run    func(context.Context, uuid.UUID, RetentionCleanupInput) (*RetentionCleanupResult, error)
}

func newRetentionRuntimeTestCleaner() *retentionRuntimeTestCleaner {
	return &retentionRuntimeTestCleaner{
		calls: make(map[uuid.UUID][]RetentionCleanupInput), errors: make(map[uuid.UUID][]error), called: make(chan uuid.UUID, 64),
	}
}

func (cleaner *retentionRuntimeTestCleaner) CleanupRetention(ctx context.Context, loggerID uuid.UUID, input RetentionCleanupInput) (*RetentionCleanupResult, error) {
	cleaner.mu.Lock()
	cleaner.calls[loggerID] = append(cleaner.calls[loggerID], input)
	callIndex := len(cleaner.calls[loggerID]) - 1
	run := cleaner.run
	var cleanupError error
	if configured := cleaner.errors[loggerID]; callIndex < len(configured) {
		cleanupError = configured[callIndex]
	}
	cleaner.mu.Unlock()
	cleaner.called <- loggerID
	if run != nil {
		return run(ctx, loggerID, input)
	}
	return &RetentionCleanupResult{EvaluatedAt: input.EvaluatedAt, Complete: cleanupError == nil}, cleanupError
}

func (cleaner *retentionRuntimeTestCleaner) awaitCallCount(t *testing.T, loggerID uuid.UUID, count int) {
	t.Helper()
	deadline := time.After(time.Second)
	for cleaner.callCount(loggerID) < count {
		select {
		case <-cleaner.called:
		case <-deadline:
			t.Fatalf("cleanup calls for %s = %d, want at least %d", loggerID, cleaner.callCount(loggerID), count)
		}
	}
}

func (cleaner *retentionRuntimeTestCleaner) callCount(loggerID uuid.UUID) int {
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	return len(cleaner.calls[loggerID])
}

func (cleaner *retentionRuntimeTestCleaner) lastInput(loggerID uuid.UUID) RetentionCleanupInput {
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	calls := cleaner.calls[loggerID]
	if len(calls) == 0 {
		return RetentionCleanupInput{}
	}
	return calls[len(calls)-1]
}

func newRetentionRuntimeTest(t *testing.T, repository RetentionRuntimeRepository, cleaner RetentionCleaner, interval time.Duration, batchLimit int) *RetentionRuntime {
	t.Helper()
	runtime, err := NewRetentionRuntime(repository, cleaner, WithRetentionRuntimeInterval(interval), WithRetentionRuntimeBatchLimit(batchLimit))
	if err != nil {
		t.Fatalf("NewRetentionRuntime() error = %v", err)
	}
	return runtime
}
