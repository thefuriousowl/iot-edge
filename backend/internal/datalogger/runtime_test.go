package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewRuntimeValidatesDependenciesAndOptions(t *testing.T) {
	t.Parallel()
	repository := &runtimeTestRepository{}
	snapshots := &schedulerSnapshotReader{}
	history := &schedulerHistoryWriter{}
	runner := newRuntimeTestRunner(nil)
	tests := []struct {
		name       string
		repository RuntimeRepository
		snapshots  SnapshotReader
		history    HistoryWriter
		options    []RuntimeOption
		want       error
	}{
		{name: "repository", snapshots: snapshots, history: history, want: ErrRuntimeRepositoryRequired},
		{name: "snapshots", repository: repository, history: history, want: ErrSnapshotReaderRequired},
		{name: "history", repository: repository, snapshots: snapshots, want: ErrHistoryWriterRequired},
		{name: "negative reconcile", repository: repository, snapshots: snapshots, history: history, options: []RuntimeOption{WithRuntimeReconcileInterval(-time.Second)}, want: ErrInvalidInput},
		{name: "missing runner", repository: repository, snapshots: snapshots, history: history, options: []RuntimeOption{WithRuntimeRunners(runner, nil)}, want: ErrRuntimeRunnerRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRuntime(test.repository, test.snapshots, test.history, test.options...); !errors.Is(err, test.want) {
				t.Fatalf("NewRuntime() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRuntimeReconcilesDefinitionsWithoutDuplicateJobs(t *testing.T) {
	t.Parallel()
	intervalID := uuid.New()
	calendarID := uuid.New()
	firstTag := TagReference{ID: uuid.New(), DataType: "float64"}
	secondTag := TagReference{ID: uuid.New(), DataType: "uint16"}
	repository := &runtimeTestRepository{loggers: []Logger{
		runtimeIntervalLogger(intervalID, "Interval", firstTag, secondTag),
		runtimeCalendarLogger(calendarID, "Calendar", firstTag),
	}}
	activity := newRuntimeTestActivity()
	intervalRunner := newRuntimeTestRunner(activity)
	calendarRunner := newRuntimeTestRunner(activity)
	runtime := newRuntimeTest(t, repository, intervalRunner, calendarRunner, 0)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)
	intervalRunner.awaitStart(t, intervalID)
	calendarRunner.awaitStart(t, calendarID)

	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(unchanged) error = %v", err)
	}
	if intervalRunner.startCount(intervalID) != 1 || calendarRunner.startCount(calendarID) != 1 {
		t.Fatalf("unchanged starts = interval %d, calendar %d", intervalRunner.startCount(intervalID), calendarRunner.startCount(calendarID))
	}

	updated := runtimeIntervalLogger(intervalID, "Renamed only", firstTag, secondTag)
	repository.setLoggers([]Logger{updated, runtimeCalendarLogger(calendarID, "Calendar", firstTag)})
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(name only) error = %v", err)
	}
	if intervalRunner.startCount(intervalID) != 1 {
		t.Fatalf("name-only update starts = %d, want 1", intervalRunner.startCount(intervalID))
	}

	updated.Config = json.RawMessage(`{"interval_seconds":2}`)
	updated.Tags = []TagReference{secondTag, firstTag}
	repository.setLoggers([]Logger{updated})
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(schedule and selection) error = %v", err)
	}
	intervalRunner.awaitStart(t, intervalID)
	if intervalRunner.startCount(intervalID) != 2 || activity.maxActive(intervalID) != 1 {
		t.Fatalf("replacement starts = %d, max active = %d", intervalRunner.startCount(intervalID), activity.maxActive(intervalID))
	}
	if activity.active(calendarID) != 0 {
		t.Fatalf("removed calendar active jobs = %d", activity.active(calendarID))
	}

	switched := runtimeCalendarLogger(intervalID, "Switched", firstTag)
	repository.setLoggers([]Logger{switched})
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(mode switch) error = %v", err)
	}
	calendarRunner.awaitStart(t, intervalID)
	if calendarRunner.startCount(intervalID) != 1 || activity.maxActive(intervalID) != 1 {
		t.Fatalf("mode switch starts = %d, max active = %d", calendarRunner.startCount(intervalID), activity.maxActive(intervalID))
	}
}

func TestRuntimeDoesNotRestartFinishedJobUntilDefinitionChanges(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	repository := &runtimeTestRepository{loggers: []Logger{runtimeIntervalLogger(loggerID, "Failing", TagReference{ID: uuid.New(), DataType: "float64"})}}
	runError := errors.New("scheduler failed")
	runner := newRuntimeTestRunner(nil)
	runner.run = func(context.Context, Logger) error { return runError }
	runtime := newRuntimeTest(t, repository, runner, newRuntimeTestRunner(nil), 0)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)
	runner.awaitStart(t, loggerID)
	select {
	case err := <-runtime.Errors():
		if !errors.Is(err, runError) {
			t.Fatalf("runtime error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not report scheduler failure")
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(unchanged failure) error = %v", err)
	}
	if runner.startCount(loggerID) != 1 {
		t.Fatalf("unchanged failed job starts = %d, want 1", runner.startCount(loggerID))
	}

	updated := repository.snapshot()[0]
	updated.Config = json.RawMessage(`{"interval_seconds":2}`)
	repository.setLoggers([]Logger{updated})
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(changed failure) error = %v", err)
	}
	runner.awaitStart(t, loggerID)
	if runner.startCount(loggerID) != 2 {
		t.Fatalf("changed failed job starts = %d, want 2", runner.startCount(loggerID))
	}
}

func TestRuntimeReportsRepositoryOutageAndRecovers(t *testing.T) {
	t.Parallel()
	repository := &runtimeTestRepository{}
	runner := newRuntimeTestRunner(nil)
	runtime := newRuntimeTest(t, repository, runner, newRuntimeTestRunner(nil), 5*time.Millisecond)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)

	repository.setError(errors.New("database unavailable"))
	select {
	case err := <-runtime.Errors():
		if !strings.Contains(err.Error(), "reconciling Data Logger runtime") || !strings.Contains(err.Error(), "database unavailable") {
			t.Fatalf("runtime error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not report repository outage")
	}
	loggerID := uuid.New()
	repository.setLoggers([]Logger{runtimeIntervalLogger(loggerID, "Recovered", TagReference{ID: uuid.New(), DataType: "float64"})})
	repository.setError(nil)
	runner.awaitStart(t, loggerID)
	time.Sleep(20 * time.Millisecond)
	if runner.startCount(loggerID) != 1 {
		t.Fatalf("recovered logger starts = %d, want 1", runner.startCount(loggerID))
	}
}

func TestRuntimeLifecycleStopsJobsCleanly(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	repository := &runtimeTestRepository{loggers: []Logger{runtimeIntervalLogger(loggerID, "Lifecycle", TagReference{ID: uuid.New(), DataType: "float64"})}}
	activity := newRuntimeTestActivity()
	runner := newRuntimeTestRunner(activity)
	runtime := newRuntimeTest(t, repository, runner, newRuntimeTestRunner(activity), 0)
	if err := runtime.Start(nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Start(nil) error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runner.awaitStart(t, loggerID)
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRuntimeAlreadyStarted) {
		t.Fatalf("Start(second) error = %v", err)
	}
	runtime.Stop()
	runtime.Stop()
	if activity.active(loggerID) != 0 {
		t.Fatalf("active jobs after Stop = %d", activity.active(loggerID))
	}
	if err := runtime.Reconcile(context.Background()); !errors.Is(err, ErrRuntimeNotStarted) {
		t.Fatalf("Reconcile(after Stop) error = %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRuntimeAlreadyStarted) {
		t.Fatalf("Start(after Stop) error = %v", err)
	}
}

type runtimeTestRepository struct {
	mu      sync.Mutex
	loggers []Logger
	err     error
}

func (repository *runtimeTestRepository) ListEnabledLoggers(context.Context) ([]Logger, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.err != nil {
		return nil, repository.err
	}
	return cloneRuntimeLoggers(repository.loggers), nil
}

func (repository *runtimeTestRepository) setLoggers(loggers []Logger) {
	repository.mu.Lock()
	repository.loggers = cloneRuntimeLoggers(loggers)
	repository.mu.Unlock()
}

func (repository *runtimeTestRepository) setError(err error) {
	repository.mu.Lock()
	repository.err = err
	repository.mu.Unlock()
}

func (repository *runtimeTestRepository) snapshot() []Logger {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return cloneRuntimeLoggers(repository.loggers)
}

func cloneRuntimeLoggers(loggers []Logger) []Logger {
	cloned := make([]Logger, len(loggers))
	for index, logger := range loggers {
		cloned[index] = logger
		cloned[index].Config = append(json.RawMessage(nil), logger.Config...)
		cloned[index].Tags = append([]TagReference(nil), logger.Tags...)
	}
	return cloned
}

type runtimeTestRunner struct {
	mu       sync.Mutex
	starts   map[uuid.UUID]int
	started  chan Logger
	run      func(context.Context, Logger) error
	activity *runtimeTestActivity
}

func newRuntimeTestRunner(activity *runtimeTestActivity) *runtimeTestRunner {
	runner := &runtimeTestRunner{starts: make(map[uuid.UUID]int), started: make(chan Logger, 32), activity: activity}
	runner.run = func(ctx context.Context, _ Logger) error {
		<-ctx.Done()
		return ctx.Err()
	}
	return runner
}

func (runner *runtimeTestRunner) Run(ctx context.Context, logger Logger) error {
	runner.mu.Lock()
	runner.starts[logger.ID]++
	runner.mu.Unlock()
	if runner.activity != nil {
		runner.activity.begin(logger.ID)
		defer runner.activity.end(logger.ID)
	}
	runner.started <- logger
	return runner.run(ctx, logger)
}

func (runner *runtimeTestRunner) awaitStart(t *testing.T, loggerID uuid.UUID) Logger {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case logger := <-runner.started:
			if logger.ID == loggerID {
				return logger
			}
		case <-deadline:
			t.Fatalf("runner did not start logger %s", loggerID)
		}
	}
}

func (runner *runtimeTestRunner) startCount(loggerID uuid.UUID) int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.starts[loggerID]
}

type runtimeTestActivity struct {
	mu         sync.Mutex
	activeJobs map[uuid.UUID]int
	maxJobs    map[uuid.UUID]int
}

func newRuntimeTestActivity() *runtimeTestActivity {
	return &runtimeTestActivity{activeJobs: make(map[uuid.UUID]int), maxJobs: make(map[uuid.UUID]int)}
}

func (activity *runtimeTestActivity) begin(loggerID uuid.UUID) {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	activity.activeJobs[loggerID]++
	if activity.activeJobs[loggerID] > activity.maxJobs[loggerID] {
		activity.maxJobs[loggerID] = activity.activeJobs[loggerID]
	}
}

func (activity *runtimeTestActivity) end(loggerID uuid.UUID) {
	activity.mu.Lock()
	activity.activeJobs[loggerID]--
	activity.mu.Unlock()
}

func (activity *runtimeTestActivity) active(loggerID uuid.UUID) int {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	return activity.activeJobs[loggerID]
}

func (activity *runtimeTestActivity) maxActive(loggerID uuid.UUID) int {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	return activity.maxJobs[loggerID]
}

func newRuntimeTest(t *testing.T, repository RuntimeRepository, interval, calendar LoggerRunner, reconcileInterval time.Duration) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(
		repository,
		&schedulerSnapshotReader{},
		&schedulerHistoryWriter{},
		WithRuntimeReconcileInterval(reconcileInterval),
		WithRuntimeRunners(interval, calendar),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime
}

func runtimeIntervalLogger(id uuid.UUID, name string, tags ...TagReference) Logger {
	return Logger{ID: id, Name: name, Enabled: true, Timezone: "UTC", Mode: ModeInterval, StartAt: time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC), Config: json.RawMessage(`{"interval_seconds":1}`), Tags: tags}
}

func runtimeCalendarLogger(id uuid.UUID, name string, tags ...TagReference) Logger {
	return Logger{ID: id, Name: name, Enabled: true, Timezone: "UTC", Mode: ModeSchedule, StartAt: time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC), Config: json.RawMessage(`{"unit":"day","every":1,"times":["10:00"]}`), Tags: tags}
}
