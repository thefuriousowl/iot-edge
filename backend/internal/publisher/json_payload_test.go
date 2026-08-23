package publisher

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJSONPayloadEngineValidatesAndRendersWhitelistedHelpers(t *testing.T) {
	engine := NewJSONPayloadEngine()
	sources := jsonPayloadTestSources()
	template := `{
		"power":{{ value "power" }},
		"rounded":{{ round "power" 2 }},
		"scaled":{{ scale "power" 100 1 }},
		"available":{{ available "power" }},
		"quality":{{ quality "power" }},
		"error":{{ error "power" }},
		"unit":{{ unit "power" }},
		"sequence":{{ sequence "power" }},
		"schema_version":{{ schema_version "power" }},
		"data_type":{{ data_type "power" }},
		"observed_at":{{ observed_at "power" }},
		"emitted_at":{{ emitted_at "power" }},
		"period_start":{{ period_start "energy" }},
		"period_end":{{ period_end "energy" }},
		"coverage":{{ coverage "energy" }},
		"state":{{ default "state" "offline" }},
		"local_time":{{ format_time "energy" "period_end" "Asia/Bangkok" "2006-01-02 15:04" }}
	}`

	validation, err := engine.Validate(template, sources)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if fmt.Sprint(validation.ReferencedAliases) != "[energy power state]" || validation.HelperCalls != 17 {
		t.Fatalf("validation metadata = %#v", validation)
	}
	for name, preview := range map[string]json.RawMessage{"good": validation.Good, "unavailable": validation.Unavailable, "windowed": validation.Windowed} {
		if !json.Valid(preview) {
			t.Errorf("%s preview is invalid JSON: %s", name, preview)
		}
	}
	if !strings.Contains(string(validation.Good), `"coverage":100`) || !strings.Contains(string(validation.Unavailable), `"power":null`) || !strings.Contains(string(validation.Windowed), `"coverage":75`) {
		t.Fatalf("representative previews = good %s, unavailable %s, windowed %s", validation.Good, validation.Unavailable, validation.Windowed)
	}

	observedAt := time.Date(2026, time.August, 23, 10, 0, 0, 123000000, time.UTC)
	emittedAt := observedAt.Add(time.Second)
	periodStart := time.Date(2026, time.August, 23, 9, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.UTC)
	coverage := 87.5
	snapshot := SourceSnapshot{CapturedAt: emittedAt, Samples: []SourceSample{
		{Alias: "power", Reference: sources[0].Descriptor.Reference, Available: true, Sequence: 9, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", Value: 12.345, Quality: SourceQualityGood, ObservedAt: &observedAt, EmittedAt: &emittedAt},
		{Alias: "state", Reference: sources[1].Descriptor.Reference, Available: false, SchemaVersion: 1, DataType: SourceDataTypeString, Quality: SourceQualityUnavailable, Error: "offline"},
		{Alias: "energy", Reference: sources[2].Descriptor.Reference, Available: true, Sequence: 4, SchemaVersion: 2, DataType: SourceDataTypeFloat64, Unit: "kWh", Value: 45.5, Quality: SourceQualityPartial, ObservedAt: &periodEnd, EmittedAt: &emittedAt, PeriodStart: &periodStart, PeriodEnd: &periodEnd, CoveragePercent: &coverage},
	}}
	rendered, err := engine.Preview(template, sources, snapshot)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	want := `{"power":12.345,"rounded":12.35,"scaled":1235.5,"available":true,"quality":"good","error":"","unit":"kW","sequence":9,"schema_version":1,"data_type":"float64","observed_at":"2026-08-23T10:00:00.123Z","emitted_at":"2026-08-23T10:00:01.123Z","period_start":"2026-08-23T09:00:00Z","period_end":"2026-08-23T10:00:00Z","coverage":87.5,"state":"offline","local_time":"2026-08-23 17:00"}`
	if string(rendered) != want {
		t.Fatalf("Preview() = %s\nwant %s", rendered, want)
	}
}

func TestJSONPayloadEngineUsesNullAndDefaultsForMissingSources(t *testing.T) {
	engine := NewJSONPayloadEngine()
	sources := jsonPayloadTestSources()[:1]
	compiled, err := engine.Compile(`{"value":{{value "power"}},"fallback":{{default "power" 99}},"quality":{{quality "power"}},"observed":{{observed_at "power"}}}`, sources)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	rendered, err := compiled.Render(SourceSnapshot{})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if string(rendered) != `{"value":null,"fallback":99,"quality":"unavailable","observed":null}` {
		t.Fatalf("missing-source payload = %s", rendered)
	}
}

func TestJSONPayloadEngineRendersPublisherContextMetadata(t *testing.T) {
	engine := NewJSONPayloadEngine()
	compiled, err := engine.Compile(`{"publisher":{{publisher_id}},"published_at":{{published_at}},"timestamp":{{published_unix_ms}}}`, jsonPayloadTestSources()[:1])
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	publisherID := uuid.MustParse("1957b293-517a-4c20-b66a-3ba933e0f9c5")
	publishedAt := time.Date(2026, time.August, 23, 8, 35, 25, 573000000, time.UTC)
	rendered, err := compiled.RenderWithContext(JSONPayloadRenderContext{PublisherID: publisherID, PublishedAt: publishedAt, Snapshot: SourceSnapshot{}})
	if err != nil {
		t.Fatalf("RenderWithContext() error = %v", err)
	}
	want := `{"publisher":"1957b293-517a-4c20-b66a-3ba933e0f9c5","published_at":"2026-08-23T08:35:25.573Z","timestamp":1787474125573}`
	if string(rendered) != want {
		t.Fatalf("metadata payload = %s, want %s", rendered, want)
	}
	validation, err := engine.Validate(`{"timestamp":{{published_unix_ms}}}`, jsonPayloadTestSources()[:1])
	if err != nil || len(validation.ReferencedAliases) != 0 || validation.HelperCalls != 1 {
		t.Fatalf("metadata validation = %#v, %v", validation, err)
	}
}

func TestJSONPayloadEngineRejectsInvalidAndUnsafeTemplates(t *testing.T) {
	engine := NewJSONPayloadEngine()
	sources := jsonPayloadTestSources()
	tests := []struct {
		name     string
		template string
		want     error
	}{
		{name: "empty", template: " ", want: ErrInvalidJSONPayloadTemplate},
		{name: "unknown alias", template: `{"x":{{value "missing"}}}`, want: ErrJSONPayloadUnknownAlias},
		{name: "unknown helper", template: `{"x":{{secret "power"}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "builtin print", template: `{"x":{{printf "%s" "secret"}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "builtin call", template: `{"x":{{call .Fn}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "dot access", template: `{"x":{{.Samples}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "if control", template: `{{if true}}{}{{end}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "range control", template: `{{range .}}{}{{end}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "named template", template: `{{define "hidden"}}{}{{end}}{{template "hidden"}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "variable", template: `{{$x := "power"}}{"x":{{value $x}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "pipeline", template: `{"x":{{value "power" | printf "%s"}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "missing argument", template: `{"x":{{value}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "extra argument", template: `{"x":{{value "power" 1}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "dynamic alias", template: `{"x":{{value .}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "round string", template: `{"x":{{round "state" 2}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "round decimal", template: `{"x":{{round "power" 2.5}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "round too precise", template: `{"x":{{round "power" 10}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "scale string", template: `{"x":{{scale "state" 2 1}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "scale nonnumeric", template: `{"x":{{scale "power" "two" 1}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "bad time field", template: `{"x":{{format_time "power" "created_at" "UTC" "2006"}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "bad timezone", template: `{"x":{{format_time "power" "observed_at" "Moon/Base" "2006"}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "empty layout", template: `{"x":{{format_time "power" "observed_at" "UTC" ""}}}`, want: ErrInvalidJSONPayloadTemplate},
		{name: "invalid JSON literal", template: `{"x":{{value "power"}}`, want: ErrJSONPayloadInvalid},
		{name: "trailing JSON", template: `{"x":1}{"y":2}`, want: ErrJSONPayloadInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := engine.Compile(test.template, sources)
			if !errors.Is(err, test.want) {
				t.Fatalf("Compile() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestJSONPayloadEngineEnforcesTemplateRenderAndJSONComplexityLimits(t *testing.T) {
	engine := NewJSONPayloadEngine()
	sources := jsonPayloadTestSources()
	if _, err := engine.Compile(strings.Repeat(" ", MaxJSONPayloadTemplateBytes+1), sources); !errors.Is(err, ErrJSONPayloadTooComplex) {
		t.Fatalf("oversized template error = %v", err)
	}
	actions := make([]string, MaxJSONPayloadActions+1)
	for index := range actions {
		actions[index] = `{{value "power"}}`
	}
	if _, err := engine.Compile(`{"x":[`+strings.Join(actions, ",")+`]}`, sources); !errors.Is(err, ErrJSONPayloadTooComplex) {
		t.Fatalf("action limit error = %v", err)
	}
	deep := strings.Repeat("[", MaxJSONPayloadDepth+1) + "0" + strings.Repeat("]", MaxJSONPayloadDepth+1)
	if _, err := engine.Compile(deep, sources); !errors.Is(err, ErrJSONPayloadTooComplex) {
		t.Fatalf("JSON depth error = %v", err)
	}

	compiled, err := engine.Compile(`{"state":{{value "state"}}}`, sources)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	large := strings.Repeat("x", MaxJSONPayloadBytes)
	snapshot := SourceSnapshot{Samples: []SourceSample{{Alias: "state", Reference: sources[1].Descriptor.Reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeString, Value: large, Quality: SourceQualityGood}}}
	if _, err := compiled.Render(snapshot); !errors.Is(err, ErrJSONPayloadTooLarge) {
		t.Fatalf("render size error = %v", err)
	}
}

func TestCompiledJSONPayloadRejectsDriftAndIsConcurrentAndImmutable(t *testing.T) {
	engine := NewJSONPayloadEngine()
	sources := jsonPayloadTestSources()[:1]
	compiled, err := engine.Compile(`{"power":{{value "power"}},"unit":{{unit "power"}}}`, sources)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	reference := sources[0].Descriptor.Reference
	sources[0].Alias = "mutated"
	sources[0].Descriptor.Unit = "W"
	snapshot := SourceSnapshot{Samples: []SourceSample{{Alias: "power", Reference: reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", Value: 5.5, Quality: SourceQualityGood}}}

	const workers = 32
	var wait sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			rendered, renderErr := compiled.Render(snapshot)
			if renderErr != nil {
				errorsChannel <- renderErr
				return
			}
			if string(rendered) != `{"power":5.5,"unit":"kW"}` {
				errorsChannel <- fmt.Errorf("unexpected payload %s", rendered)
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for renderErr := range errorsChannel {
		t.Error(renderErr)
	}

	drift := snapshot
	drift.Samples = append([]SourceSample(nil), snapshot.Samples...)
	drift.Samples[0].SchemaVersion = 2
	if _, err := compiled.Render(drift); !errors.Is(err, ErrJSONPayloadRender) {
		t.Fatalf("schema drift error = %v", err)
	}
	duplicate := snapshot
	duplicate.Samples = append(duplicate.Samples, duplicate.Samples[0])
	if _, err := compiled.Render(duplicate); !errors.Is(err, ErrJSONPayloadRender) {
		t.Fatalf("duplicate alias error = %v", err)
	}
	typeMismatch := snapshot
	typeMismatch.Samples = append([]SourceSample(nil), snapshot.Samples...)
	typeMismatch.Samples[0].Value = "5.5"
	if _, err := compiled.Render(typeMismatch); !errors.Is(err, ErrJSONPayloadRender) {
		t.Fatalf("value type drift error = %v", err)
	}
	nonfinite := snapshot
	nonfinite.Samples = append([]SourceSample(nil), snapshot.Samples...)
	nonfinite.Samples[0].Value = math.NaN()
	if _, err := compiled.Render(nonfinite); !errors.Is(err, ErrJSONPayloadRender) {
		t.Fatalf("non-finite value error = %v", err)
	}
	badWithoutValue := snapshot
	badWithoutValue.Samples = append([]SourceSample(nil), snapshot.Samples...)
	badWithoutValue.Samples[0].Value = nil
	badWithoutValue.Samples[0].Quality = SourceQualityBad
	if rendered, err := compiled.Render(badWithoutValue); err != nil || string(rendered) != `{"power":null,"unit":"kW"}` {
		t.Fatalf("bad null sample = %s, %v", rendered, err)
	}
}

func TestJSONPayloadEngineValidatesSourceDefinitions(t *testing.T) {
	engine := NewJSONPayloadEngine()
	valid := jsonPayloadTestSources()[:1]
	tests := []struct {
		name    string
		sources []ResolvedSource
		want    error
	}{
		{name: "missing", want: ErrInvalidSourceSelection},
		{name: "duplicate", sources: append(append([]ResolvedSource(nil), valid...), valid[0]), want: ErrDuplicateSourceAlias},
		{name: "bad alias", sources: []ResolvedSource{{Alias: "bad alias", Descriptor: valid[0].Descriptor}}, want: ErrInvalidSourceSelection},
		{name: "bad reference", sources: []ResolvedSource{{Alias: "power", Descriptor: SourceDescriptor{SchemaVersion: 1, DataType: SourceDataTypeFloat64}}}, want: ErrInvalidSourceSelection},
		{name: "bad schema", sources: []ResolvedSource{{Alias: "power", Descriptor: SourceDescriptor{Reference: valid[0].Descriptor.Reference, DataType: SourceDataTypeFloat64}}}, want: ErrInvalidSourceSelection},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := engine.Compile(`{}`, test.sources)
			if !errors.Is(err, test.want) {
				t.Fatalf("Compile() error = %v, want %v", err, test.want)
			}
		})
	}
}

func jsonPayloadTestSources() []ResolvedSource {
	return []ResolvedSource{
		{Alias: "power", Descriptor: SourceDescriptor{Reference: TagSource(uuid.New()), Name: "Power", SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", PeriodKind: SourcePeriodInstantaneous, Enabled: true}},
		{Alias: "state", Descriptor: SourceDescriptor{Reference: TagSource(uuid.New()), Name: "State", SchemaVersion: 1, DataType: SourceDataTypeString, PeriodKind: SourcePeriodInstantaneous, Enabled: true}},
		{Alias: "energy", Descriptor: SourceDescriptor{Reference: PluginOutputSource(uuid.New(), "today.energy_kwh"), Name: "Energy", SchemaVersion: 2, DataType: SourceDataTypeFloat64, Unit: "kWh", PeriodKind: SourcePeriodWindowed, Enabled: true}},
	}
}

func FuzzJSONPayloadEngineCompileAndRender(f *testing.F) {
	for _, seed := range []string{
		`{"value":{{value "power"}}}`,
		`{"value":{{default "power" 0}},"quality":{{quality "power"}}}`,
		`{{if true}}{{call .Fn}}{{end}}`,
		`{"time":{{format_time "energy" "period_end" "Asia/Bangkok" "2006-01-02"}}}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > MaxJSONPayloadTemplateBytes+1 {
			return
		}
		engine := NewJSONPayloadEngine()
		compiled, err := engine.Compile(raw, jsonPayloadTestSources())
		if err != nil {
			return
		}
		rendered, err := compiled.Render(jsonPayloadFixture(compiled.sources, jsonPayloadFixtureWindowed))
		if err != nil {
			t.Fatalf("validated template did not render: %v", err)
		}
		if !json.Valid(rendered) || len(rendered) > MaxJSONPayloadBytes {
			t.Fatalf("invalid bounded payload: %q", rendered)
		}
	})
}
