package plugin

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type managerRuntimeFunction func(context.Context) error

func (runtime managerRuntimeFunction) Run(ctx context.Context) error { return runtime(ctx) }

type managerBlockingRuntime struct {
	started chan struct{}
	stopped chan struct{}
}

func newManagerBlockingRuntime() *managerBlockingRuntime {
	return &managerBlockingRuntime{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (runtime *managerBlockingRuntime) Run(ctx context.Context) error {
	close(runtime.started)
	defer close(runtime.stopped)
	<-ctx.Done()
	return ctx.Err()
}

func TestNewManagerRequiresDependenciesAndValidOptions(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	if _, err := NewManager(nil, registry, nil); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("NewManager(nil repository) error = %v", err)
	}
	var typedNil *pluginMemoryRepository
	if _, err := NewManager(typedNil, registry, nil); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("NewManager(typed nil repository) error = %v", err)
	}
	if _, err := NewManager(repository, nil, nil); !errors.Is(err, ErrRegistryRequired) {
		t.Fatalf("NewManager(nil registry) error = %v", err)
	}
	if _, err := NewManager(repository, registry, nil, WithManagerReconcileInterval(-time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewManager(negative interval) error = %v", err)
	}
	if _, err := NewManager(repository, registry, nil, WithManagerClock(nil)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewManager(nil clock) error = %v", err)
	}
	manager, err := NewManager(repository, registry, nil, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Start(nil) error = %v", err)
	}
	if err := manager.Reconcile(context.Background()); !errors.Is(err, ErrManagerNotStarted) {
		t.Errorf("Reconcile(before start) error = %v", err)
	}
	if err := manager.Restart(context.Background(), uuid.New()); !errors.Is(err, ErrManagerNotStarted) {
		t.Errorf("Restart(before start) error = %v", err)
	}
	if err := manager.Restart(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Restart(nil ID) error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Errorf("Stop(before start) error = %v", err)
	}
}

func TestManagerReconcilesConfigLifecycleAndScopesCapabilities(t *testing.T) {
	t.Parallel()

	runtimes := make(chan *managerBlockingRuntime, 8)
	definition := validRegistryDefinition("energy_management")
	definition.manifest.Capabilities = []Capability{"logger.history"}
	definition.build = func(_ RuntimeSpec, host Host) (Runtime, error) {
		if value, exists := host.ResolveCapability("logger.history"); !exists || value != "history" {
			t.Fatalf("declared capability = %#v, %t", value, exists)
		}
		if value, exists := host.ResolveCapability("secrets"); exists || value != nil {
			t.Fatalf("undeclared capability = %#v, %t", value, exists)
		}
		runtime := newManagerBlockingRuntime()
		runtimes <- runtime
		return runtime, nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	host, err := NewCapabilityHost(map[Capability]any{"logger.history": "history", "secrets": "secret"})
	if err != nil {
		t.Fatalf("NewCapabilityHost() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	instance := Instance{ID: uuid.New(), Type: "energy_management", Name: "Plant", Enabled: true, Config: Config(`{"logger":"a"}`), ConfigVersion: 1}
	if err := repository.Create(context.Background(), &instance); err != nil {
		t.Fatalf("repository.Create() error = %v", err)
	}
	manager, err := NewManager(repository, registry, host, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	first := receiveManagerRuntime(t, runtimes)
	waitChannel(t, first.started, "first runtime start")
	assertManagerState(t, manager, instance.ID, RuntimeStateRunning, nil)

	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(unchanged) error = %v", err)
	}
	assertNoManagerRuntime(t, runtimes)
	repository.mutate(instance.ID, func(stored *Instance) { stored.Name = "Renamed" })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(name only) error = %v", err)
	}
	assertNoManagerRuntime(t, runtimes)

	repository.mutate(instance.ID, func(stored *Instance) { stored.Config = Config(`{"logger":"b"}`) })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(config change) error = %v", err)
	}
	waitChannel(t, first.stopped, "first runtime stop")
	second := receiveManagerRuntime(t, runtimes)
	waitChannel(t, second.started, "second runtime start")
	assertManagerState(t, manager, instance.ID, RuntimeStateRunning, nil)

	repository.mutate(instance.ID, func(stored *Instance) { stored.Enabled = false })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(disabled) error = %v", err)
	}
	waitChannel(t, second.stopped, "disabled runtime stop")
	assertManagerState(t, manager, instance.ID, RuntimeStateStopped, nil)

	repository.mutate(instance.ID, func(stored *Instance) { stored.Enabled = true })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(re-enabled) error = %v", err)
	}
	third := receiveManagerRuntime(t, runtimes)
	waitChannel(t, third.started, "third runtime start")
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitChannel(t, third.stopped, "third runtime stop")
	assertManagerState(t, manager, instance.ID, RuntimeStateStopped, nil)
	if err := manager.Stop(context.Background()); err != nil {
		t.Errorf("Stop(idempotent) error = %v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrManagerAlreadyStarted) {
		t.Errorf("Start(after stop) error = %v", err)
	}
}

func TestManagerKeepsFailuresStableUntilExplicitRestart(t *testing.T) {
	t.Parallel()

	runtimeFailure := errors.New("subscription failed")
	var attempts atomic.Int32
	secondRuntime := newManagerBlockingRuntime()
	definition := validRegistryDefinition("energy_management")
	definition.build = func(RuntimeSpec, Host) (Runtime, error) {
		if attempts.Add(1) == 1 {
			return managerRuntimeFunction(func(context.Context) error { return runtimeFailure }), nil
		}
		return secondRuntime, nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	instance := Instance{ID: uuid.New(), Type: "energy_management", Name: "Energy", Enabled: true, Config: Config(`{}`), ConfigVersion: 1}
	if err := repository.Create(context.Background(), &instance); err != nil {
		t.Fatalf("repository.Create() error = %v", err)
	}
	manager, err := NewManager(repository, registry, nil, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitManagerState(t, manager, instance.ID, RuntimeStateError)
	status := manager.Status(instance.ID)
	if !errors.Is(status.LastError, runtimeFailure) {
		t.Fatalf("LastError = %v", status.LastError)
	}
	select {
	case reported := <-manager.Errors():
		if !errors.Is(reported, runtimeFailure) {
			t.Fatalf("Errors() = %v", reported)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime error")
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(failed unchanged) error = %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts after unchanged reconcile = %d", attempts.Load())
	}
	if err := manager.Restart(context.Background(), instance.ID); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	waitChannel(t, secondRuntime.started, "retried runtime start")
	if attempts.Load() != 2 {
		t.Fatalf("attempts after restart = %d", attempts.Load())
	}
	assertManagerState(t, manager, instance.ID, RuntimeStateRunning, nil)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagerIsolatesDefinitionAndRuntimeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		instanceType Type
		version      uint
		configure    func(*registryDefinition)
		host         Host
		want         error
	}{
		{name: "unknown type", instanceType: "unknown", version: 1, want: ErrNotRegistered},
		{name: "config version mismatch", instanceType: "energy_management", version: 2, want: ErrConfigVersionMismatch},
		{name: "missing capability", instanceType: "energy_management", version: 1, configure: func(definition *registryDefinition) {
			definition.manifest.Capabilities = []Capability{"logger.history"}
		}, want: ErrCapabilityUnavailable},
		{name: "build panic", instanceType: "energy_management", version: 1, configure: func(definition *registryDefinition) {
			definition.build = func(RuntimeSpec, Host) (Runtime, error) { panic("build panic") }
		}, want: ErrRuntimePanicked},
		{name: "runtime panic", instanceType: "energy_management", version: 1, configure: func(definition *registryDefinition) {
			definition.build = func(RuntimeSpec, Host) (Runtime, error) {
				return managerRuntimeFunction(func(context.Context) error { panic("run panic") }), nil
			}
		}, want: ErrRuntimePanicked},
		{name: "clean early exit", instanceType: "energy_management", version: 1, configure: func(definition *registryDefinition) {
			definition.build = func(RuntimeSpec, Host) (Runtime, error) {
				return managerRuntimeFunction(func(context.Context) error { return nil }), nil
			}
		}, want: ErrRuntimeExited},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validRegistryDefinition("energy_management")
			if test.configure != nil {
				test.configure(definition)
			}
			registry, err := NewRegistry(definition)
			if err != nil {
				t.Fatalf("NewRegistry() error = %v", err)
			}
			repository := newPluginMemoryRepository()
			instance := Instance{ID: uuid.New(), Type: test.instanceType, Name: test.name, Enabled: true, Config: Config(`{}`), ConfigVersion: test.version}
			if err := repository.Create(context.Background(), &instance); err != nil {
				t.Fatalf("repository.Create() error = %v", err)
			}
			manager, err := NewManager(repository, registry, test.host, WithManagerReconcileInterval(0))
			if err != nil {
				t.Fatalf("NewManager() error = %v", err)
			}
			if err := manager.Start(context.Background()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			waitManagerState(t, manager, instance.ID, RuntimeStateError)
			if status := manager.Status(instance.ID); !errors.Is(status.LastError, test.want) {
				t.Fatalf("LastError = %v, want %v", status.LastError, test.want)
			}
			if err := manager.Stop(context.Background()); err != nil {
				t.Fatalf("Stop() error = %v", err)
			}
		})
	}
}

func TestManagerStopHonorsDeadlineForRuntimeIgnoringCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	definition := validRegistryDefinition("energy_management")
	definition.build = func(RuntimeSpec, Host) (Runtime, error) {
		return managerRuntimeFunction(func(context.Context) error {
			close(started)
			<-release
			return nil
		}), nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	instance := Instance{ID: uuid.New(), Type: "energy_management", Name: "Stubborn", Enabled: true, Config: Config(`{}`), ConfigVersion: 1}
	if err := repository.Create(context.Background(), &instance); err != nil {
		t.Fatalf("repository.Create() error = %v", err)
	}
	manager, err := NewManager(repository, registry, nil, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitChannel(t, started, "stubborn runtime start")
	stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := manager.Stop(stopContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop(timeout) error = %v", err)
	}
	close(release)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(after release) error = %v", err)
	}
	assertManagerState(t, manager, instance.ID, RuntimeStateStopped, nil)
}

func TestManagerPeriodicallyDiscoversEnabledInstancesAndReportsRepositoryFailures(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	definition := validRegistryDefinition("energy_management")
	definition.build = func(RuntimeSpec, Host) (Runtime, error) {
		return managerRuntimeFunction(func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}), nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	manager, err := NewManager(repository, registry, nil, WithManagerReconcileInterval(5*time.Millisecond))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	instance := Instance{ID: uuid.New(), Type: "energy_management", Name: "Discovered", Enabled: true, Config: Config(`{}`), ConfigVersion: 1}
	if err := repository.Create(context.Background(), &instance); err != nil {
		t.Fatalf("repository.Create() error = %v", err)
	}
	waitChannel(t, started, "periodically discovered runtime")
	repository.mutex.Lock()
	repository.err = errors.New("database offline")
	repository.mutex.Unlock()
	select {
	case reported := <-manager.Errors():
		if !strings.Contains(reported.Error(), "database offline") {
			t.Fatalf("Errors() = %v", reported)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconcile error")
	}
	repository.mutex.Lock()
	repository.err = nil
	repository.mutex.Unlock()
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagerStartFailureStopsManager(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	repository.err = errors.New("database unavailable")
	manager, err := NewManager(repository, registry, nil, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, repository.err) {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Reconcile(context.Background()); !errors.Is(err, ErrManagerNotStarted) {
		t.Fatalf("Reconcile(after failed start) error = %v", err)
	}
}

func receiveManagerRuntime(t *testing.T, runtimes <-chan *managerBlockingRuntime) *managerBlockingRuntime {
	t.Helper()
	select {
	case runtime := <-runtimes:
		return runtime
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime creation")
		return nil
	}
}

func assertNoManagerRuntime(t *testing.T, runtimes <-chan *managerBlockingRuntime) {
	t.Helper()
	select {
	case <-runtimes:
		t.Fatal("unexpected runtime creation")
	case <-time.After(15 * time.Millisecond):
	}
}

func waitChannel(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitManagerState(t *testing.T, manager *Manager, id uuid.UUID, state RuntimeState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if manager.Status(id).State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status = %#v, want %s", manager.Status(id), state)
}

func assertManagerState(t *testing.T, manager *Manager, id uuid.UUID, state RuntimeState, lastError error) {
	t.Helper()
	status := manager.Status(id)
	if status.InstanceID != id || status.State != state {
		t.Fatalf("Status() = %#v, want %s", status, state)
	}
	if lastError == nil && status.LastError != nil {
		t.Errorf("LastError = %v, want nil", status.LastError)
	}
}
