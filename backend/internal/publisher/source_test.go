package publisher

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceReferenceValidatesAndUsesDiscriminatedJSON(t *testing.T) {
	t.Parallel()

	tagID := uuid.New()
	instanceID := uuid.New()
	tagReference := TagSource(tagID)
	pluginReference := PluginOutputSource(instanceID, "today.energy_kwh")
	if err := tagReference.Validate(); err != nil {
		t.Fatalf("Tag reference error = %v", err)
	}
	if err := pluginReference.Validate(); err != nil {
		t.Fatalf("Plugin output reference error = %v", err)
	}
	if tagReference.String() != "tag:"+tagID.String() {
		t.Errorf("Tag reference string = %q", tagReference.String())
	}
	if pluginReference.String() != "plugin_output:"+instanceID.String()+":today.energy_kwh" {
		t.Errorf("Plugin reference string = %q", pluginReference.String())
	}

	for _, test := range []struct {
		name      string
		reference SourceReference
		wantKeys  []string
	}{
		{name: "Tag", reference: tagReference, wantKeys: []string{"kind", "tag_id"}},
		{name: "Plugin output", reference: pluginReference, wantKeys: []string{"kind", "plugin_instance_id", "output_key"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.reference)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &object); err != nil {
				t.Fatalf("Unmarshal map error = %v", err)
			}
			if len(object) != len(test.wantKeys) {
				t.Fatalf("JSON keys = %v, want %v", reflect.ValueOf(object).MapKeys(), test.wantKeys)
			}
			for _, key := range test.wantKeys {
				if _, exists := object[key]; !exists {
					t.Errorf("JSON missing key %q: %s", key, encoded)
				}
			}
			var decoded SourceReference
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if decoded != test.reference {
				t.Errorf("decoded = %#v, want %#v", decoded, test.reference)
			}
		})
	}

	invalid := []SourceReference{
		{},
		{Kind: SourceKindTag},
		{Kind: SourceKindTag, TagID: tagID, PluginInstanceID: instanceID},
		{Kind: SourceKindPluginOutput, PluginInstanceID: instanceID},
		{Kind: SourceKindPluginOutput, PluginInstanceID: instanceID, OutputKey: "Invalid Key"},
		{Kind: "datasource", TagID: tagID},
	}
	for _, reference := range invalid {
		if err := reference.Validate(); !errors.Is(err, ErrInvalidSourceReference) {
			t.Errorf("Validate(%#v) error = %v", reference, err)
		}
		if _, err := json.Marshal(reference); !errors.Is(err, ErrInvalidSourceReference) {
			t.Errorf("Marshal(%#v) error = %v", reference, err)
		}
	}

	for _, raw := range []string{
		`{"kind":"tag"}`,
		`{"kind":"tag","tag_id":"` + tagID.String() + `","output_key":"metric"}`,
		`{"kind":"tag","tag_id":"` + tagID.String() + `","unknown":true}`,
		`{"kind":"plugin_output","plugin_instance_id":"` + instanceID.String() + `"}`,
		`{"kind":"plugin_output","plugin_instance_id":"` + instanceID.String() + `","output_key":"Invalid Key"}`,
	} {
		var reference SourceReference
		if err := json.Unmarshal([]byte(raw), &reference); !errors.Is(err, ErrInvalidSourceReference) {
			t.Errorf("Unmarshal(%s) error = %v", raw, err)
		}
	}
}

func TestNormalizeSourceSelectionsRejectsUnsafeOrAmbiguousSelections(t *testing.T) {
	t.Parallel()

	tagID := uuid.New()
	instanceID := uuid.New()
	valid := []SourceSelection{
		{Alias: "line_voltage", Reference: TagSource(tagID)},
		{Alias: "Energy.Today", Reference: PluginOutputSource(instanceID, "today.energy_kwh")},
	}
	normalized, err := NormalizeSourceSelections(valid)
	if err != nil {
		t.Fatalf("NormalizeSourceSelections() error = %v", err)
	}
	valid[0].Alias = "mutated"
	if normalized[0].Alias != "line_voltage" {
		t.Fatalf("normalized selection aliases caller state: %#v", normalized)
	}

	tests := []struct {
		name       string
		selections []SourceSelection
		want       error
	}{
		{name: "empty", want: ErrInvalidSourceSelection},
		{name: "blank alias", selections: []SourceSelection{{Reference: TagSource(tagID)}}, want: ErrInvalidSourceSelection},
		{name: "whitespace", selections: []SourceSelection{{Alias: " voltage ", Reference: TagSource(tagID)}}, want: ErrInvalidSourceSelection},
		{name: "unsafe alias", selections: []SourceSelection{{Alias: "line voltage", Reference: TagSource(tagID)}}, want: ErrInvalidSourceSelection},
		{name: "invalid reference", selections: []SourceSelection{{Alias: "voltage", Reference: SourceReference{}}}, want: ErrInvalidSourceSelection},
		{name: "duplicate alias", selections: []SourceSelection{{Alias: "value", Reference: TagSource(tagID)}, {Alias: "value", Reference: PluginOutputSource(instanceID, "metric")}}, want: ErrDuplicateSourceAlias},
		{name: "duplicate source", selections: []SourceSelection{{Alias: "first", Reference: TagSource(tagID)}, {Alias: "second", Reference: TagSource(tagID)}}, want: ErrDuplicateSource},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeSourceSelections(test.selections); !errors.Is(err, test.want) {
				t.Fatalf("NormalizeSourceSelections() error = %v, want %v", err, test.want)
			}
		})
	}

	tooMany := make([]SourceSelection, MaxSourceSelections+1)
	for index := range tooMany {
		tooMany[index] = SourceSelection{Alias: "source_" + strings.Repeat("x", index%20) + string(rune('A'+index%26)), Reference: TagSource(uuid.New())}
	}
	if _, err := NormalizeSourceSelections(tooMany); !errors.Is(err, ErrInvalidSourceSelection) {
		t.Errorf("too many selections error = %v", err)
	}
}

func TestCloneSourceSampleDefensivelyCopiesProvenance(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.UTC)
	originalAt := at
	coverage := 75.0
	sample := SourceSample{
		Reference: PluginOutputSource(uuid.New(), "metric"), Available: true,
		ObservedAt: &at, EmittedAt: &at, PeriodStart: &at, PeriodEnd: &at,
		CoveragePercent: &coverage,
		Issues:          []SourceIssue{{Code: "gap", Message: "missing", PeriodStart: &at, PeriodEnd: &at}},
		Attributes:      map[string]string{"timezone": "Asia/Bangkok"},
		Batch:           &SourceBatchIdentity{Kind: SourceBatchPluginOutput, ID: "batch", Sequence: 1},
	}
	cloned := cloneSourceSample(sample)
	*sample.ObservedAt = at.Add(time.Hour)
	*sample.CoveragePercent = 1
	sample.Issues[0].Message = "mutated"
	*sample.Issues[0].PeriodStart = at.Add(time.Hour)
	sample.Attributes["timezone"] = "UTC"
	sample.Batch.ID = "mutated"
	if !cloned.ObservedAt.Equal(originalAt) || *cloned.CoveragePercent != 75 || cloned.Issues[0].Message != "missing" || !cloned.Issues[0].PeriodStart.Equal(originalAt) || cloned.Attributes["timezone"] != "Asia/Bangkok" || cloned.Batch.ID != "batch" {
		t.Fatalf("clone aliases source state: %#v", cloned)
	}
}
