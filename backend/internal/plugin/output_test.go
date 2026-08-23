package plugin

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateOutputDescriptorsAcceptsSupportedTypesAndPeriodKinds(t *testing.T) {
	t.Parallel()

	types := []OutputDataType{
		OutputDataTypeBool,
		OutputDataTypeInt16,
		OutputDataTypeUInt16,
		OutputDataTypeInt32,
		OutputDataTypeUInt32,
		OutputDataTypeFloat32,
		OutputDataTypeFloat64,
		OutputDataTypeString,
	}
	descriptors := make([]OutputDescriptor, 0, len(types))
	for index, dataType := range types {
		descriptor := validOutputDescriptor(OutputKey("output_" + string(rune('a'+index))))
		descriptor.DataType = dataType
		if index%2 == 1 {
			descriptor.PeriodKind = OutputPeriodWindowed
		}
		descriptors = append(descriptors, descriptor)
	}
	if err := ValidateOutputDescriptors(descriptors); err != nil {
		t.Fatalf("ValidateOutputDescriptors() error = %v", err)
	}
	if err := ValidateOutputDescriptors(nil); err != nil {
		t.Fatalf("ValidateOutputDescriptors(nil) error = %v", err)
	}
}

func TestValidateOutputDescriptorsRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	base := validOutputDescriptor("electrical_demand_kw")
	tests := []struct {
		name        string
		descriptors []OutputDescriptor
	}{
		{name: "empty key", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Key = "" })}},
		{name: "uppercase key", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Key = "Power" })}},
		{name: "duplicate key", descriptors: []OutputDescriptor{base, base}},
		{name: "empty name", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Name = "" })}},
		{name: "untrimmed name", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Name = " Power " })}},
		{name: "long name", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Name = strings.Repeat("x", maxOutputNameLength+1) })}},
		{name: "untrimmed description", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Description = " Description " })}},
		{name: "long description", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Description = strings.Repeat("x", maxOutputDescription+1) })}},
		{name: "zero schema", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.SchemaVersion = 0 })}},
		{name: "unknown data type", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.DataType = "decimal" })}},
		{name: "untrimmed unit", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Unit = " kW " })}},
		{name: "long unit", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.Unit = strings.Repeat("x", maxOutputUnitLength+1) })}},
		{name: "dynamic fixed unit", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.DynamicUnit = true })}},
		{name: "unknown period", descriptors: []OutputDescriptor{mutateOutputDescriptor(base, func(value *OutputDescriptor) { value.PeriodKind = "rolling" })}},
	}
	tooMany := make([]OutputDescriptor, maxPluginOutputs+1)
	for index := range tooMany {
		tooMany[index] = validOutputDescriptor(OutputKey("output." + strings.Repeat("a", index/26) + string(rune('a'+index%26))))
	}
	tests = append(tests, struct {
		name        string
		descriptors []OutputDescriptor
	}{name: "too many", descriptors: tooMany})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateOutputDescriptors(test.descriptors); !errors.Is(err, ErrInvalidOutputDescriptor) {
				t.Fatalf("ValidateOutputDescriptors() error = %v, want ErrInvalidOutputDescriptor", err)
			}
		})
	}
}

func TestNormalizeOutputBatchPreservesPeriodProvenanceAndSnapshotsMutableValues(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("ICT", 7*60*60)
	periodStart := time.Date(2026, 8, 23, 8, 0, 0, 0, location)
	periodEnd := periodStart.Add(time.Hour)
	issueStart, issueEnd := periodStart.Add(20*time.Minute), periodStart.Add(25*time.Minute)
	expectedIssueStart := issueStart
	coverage := 91.25
	descriptors := []OutputDescriptor{
		validOutputDescriptor("electrical_demand_kw"),
		{
			Key: "estimated_cost", Name: "Estimated cost", SchemaVersion: 2,
			DataType: OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: OutputPeriodWindowed,
		},
	}
	batch := OutputBatch{
		InstanceID: uuid.New(), Sequence: 42, PublishedAt: periodEnd.Add(time.Second),
		Values: []OutputValue{
			{
				Key: "electrical_demand_kw", SchemaVersion: 1, DataType: OutputDataTypeFloat64, Unit: "kW",
				Value: 23.5, Quality: OutputQualityGood, ObservedAt: periodEnd,
				PeriodStart: periodEnd, PeriodEnd: periodEnd,
			},
			{
				Key: "estimated_cost", SchemaVersion: 2, DataType: OutputDataTypeFloat64, Unit: "THB",
				Value: 105.75, Quality: OutputQualityPartial, ObservedAt: periodEnd,
				PeriodStart: periodStart, PeriodEnd: periodEnd, CoveragePercent: &coverage,
				Issues:     []OutputIssue{{Code: "no_coverage", Message: "Logger history is incomplete", Source: "energy", PeriodStart: &issueStart, PeriodEnd: &issueEnd}},
				Attributes: map[string]string{"currency": "THB", "timezone": "Asia/Bangkok"},
			},
		},
	}

	normalized, err := NormalizeOutputBatch(batch, descriptors)
	if err != nil {
		t.Fatalf("NormalizeOutputBatch() error = %v", err)
	}
	if normalized.PublishedAt.Location() != time.UTC || normalized.Values[0].ObservedAt.Location() != time.UTC || normalized.Values[1].PeriodStart.Location() != time.UTC {
		t.Fatalf("normalized timestamps are not UTC: %#v", normalized)
	}
	windowed := normalized.Values[1]
	if !windowed.PeriodStart.Equal(periodStart) || !windowed.PeriodEnd.Equal(periodEnd) || windowed.CoveragePercent == nil || *windowed.CoveragePercent != 91.25 {
		t.Fatalf("window provenance = %#v", windowed)
	}

	coverage = 1
	batch.Values[1].Attributes["currency"] = "USD"
	*batch.Values[1].Issues[0].PeriodStart = periodStart
	if *normalized.Values[1].CoveragePercent != 91.25 || normalized.Values[1].Attributes["currency"] != "THB" || !normalized.Values[1].Issues[0].PeriodStart.Equal(expectedIssueStart) {
		t.Fatalf("NormalizeOutputBatch() aliases caller state: %#v", normalized.Values[1])
	}

	cloned := CloneOutputBatch(normalized)
	*cloned.Values[1].CoveragePercent = 50
	cloned.Values[1].Attributes["currency"] = "USD"
	*cloned.Values[1].Issues[0].PeriodEnd = periodEnd
	if *normalized.Values[1].CoveragePercent != 91.25 || normalized.Values[1].Attributes["currency"] != "THB" || !normalized.Values[1].Issues[0].PeriodEnd.Equal(issueEnd) {
		t.Fatalf("CloneOutputBatch() aliases original state: %#v", normalized.Values[1])
	}

	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, expected := range []string{"\"period_start\"", "\"period_end\"", "\"coverage_percent\":91.25", "\"quality\":\"partial\""} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("JSON missing %s: %s", expected, encoded)
		}
	}
}

func TestNormalizeOutputBatchAcceptsTypedValuesAndQualityStates(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		dataType OutputDataType
		value    any
	}{
		{name: "bool", dataType: OutputDataTypeBool, value: true},
		{name: "int16", dataType: OutputDataTypeInt16, value: int16(-1)},
		{name: "uint16", dataType: OutputDataTypeUInt16, value: uint16(1)},
		{name: "int32", dataType: OutputDataTypeInt32, value: int32(-2)},
		{name: "uint32", dataType: OutputDataTypeUInt32, value: uint32(2)},
		{name: "float32", dataType: OutputDataTypeFloat32, value: float32(1.25)},
		{name: "float64", dataType: OutputDataTypeFloat64, value: 2.5},
		{name: "string", dataType: OutputDataTypeString, value: "running"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := validOutputDescriptor("metric")
			descriptor.DataType = test.dataType
			batch := validInstantaneousBatch(at)
			batch.Values[0].DataType = test.dataType
			batch.Values[0].Value = test.value
			if _, err := NormalizeOutputBatch(batch, []OutputDescriptor{descriptor}); err != nil {
				t.Fatalf("NormalizeOutputBatch() error = %v", err)
			}
		})
	}

	coverage := 50.0
	partial := validWindowedBatch(at)
	partial.Values[0].Quality = OutputQualityPartial
	partial.Values[0].CoveragePercent = &coverage
	if _, err := NormalizeOutputBatch(partial, []OutputDescriptor{validWindowedDescriptor("metric")}); err != nil {
		t.Fatalf("NormalizeOutputBatch(partial) error = %v", err)
	}
	bad := validInstantaneousBatch(at)
	bad.Values[0].Value = nil
	bad.Values[0].Quality = OutputQualityBad
	bad.Values[0].Error = "Modbus 0x02 Illegal Data Address"
	if _, err := NormalizeOutputBatch(bad, []OutputDescriptor{validOutputDescriptor("metric")}); err != nil {
		t.Fatalf("NormalizeOutputBatch(bad) error = %v", err)
	}
}

func TestOutputValueJSONRoundTripPreservesConcreteTypesAndNullBadValues(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	values := []OutputValue{
		{Key: "bool", SchemaVersion: 1, DataType: OutputDataTypeBool, Value: true},
		{Key: "int16", SchemaVersion: 1, DataType: OutputDataTypeInt16, Value: int16(-16)},
		{Key: "uint16", SchemaVersion: 1, DataType: OutputDataTypeUInt16, Value: uint16(16)},
		{Key: "int32", SchemaVersion: 1, DataType: OutputDataTypeInt32, Value: int32(-32)},
		{Key: "uint32", SchemaVersion: 1, DataType: OutputDataTypeUInt32, Value: uint32(32)},
		{Key: "float32", SchemaVersion: 1, DataType: OutputDataTypeFloat32, Value: float32(1.25)},
		{Key: "float64", SchemaVersion: 1, DataType: OutputDataTypeFloat64, Value: 2.5},
		{Key: "string", SchemaVersion: 1, DataType: OutputDataTypeString, Value: "running"},
		{Key: "bad", SchemaVersion: 1, DataType: OutputDataTypeFloat64, Value: nil, Quality: OutputQualityBad, Error: "source failed"},
	}
	for index := range values {
		values[index].Quality = OutputQualityGood
		values[index].ObservedAt = at
		values[index].PeriodStart = at
		values[index].PeriodEnd = at
	}
	values[len(values)-1].Quality = OutputQualityBad
	values[len(values)-1].Error = "source failed"
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded []OutputValue
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for index := range values[:len(values)-1] {
		if reflect.TypeOf(decoded[index].Value) != reflect.TypeOf(values[index].Value) || !reflect.DeepEqual(decoded[index].Value, values[index].Value) {
			t.Errorf("decoded %s = %#v (%T), want %#v (%T)", values[index].Key, decoded[index].Value, decoded[index].Value, values[index].Value, values[index].Value)
		}
	}
	if decoded[len(decoded)-1].Value != nil {
		t.Errorf("decoded bad value = %#v, want nil", decoded[len(decoded)-1].Value)
	}
	for _, invalid := range []string{
		`{"data_type":"int16","value":40000}`,
		`{"data_type":"float64","value":"not-a-number"}`,
		`{"data_type":"decimal","value":1}`,
		`{"data_type":"float64"}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &OutputValue{}); !errors.Is(err, ErrInvalidOutputBatch) {
			t.Errorf("json.Unmarshal(%s) error = %v, want ErrInvalidOutputBatch", invalid, err)
		}
	}
}

func TestNormalizeOutputBatchRejectsInvalidValuesAndProvenance(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	validInstant := validOutputDescriptor("metric")
	validWindow := validWindowedDescriptor("metric")
	coverage := 50.0
	issueStart, issueEnd := at.Add(-50*time.Minute), at.Add(-40*time.Minute)
	tests := []struct {
		name        string
		descriptors []OutputDescriptor
		batch       OutputBatch
	}{
		{name: "invalid descriptors", descriptors: []OutputDescriptor{{}}, batch: validInstantaneousBatch(at)},
		{name: "nil instance", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.InstanceID = uuid.Nil })},
		{name: "zero sequence", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Sequence = 0 })},
		{name: "zero published", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.PublishedAt = time.Time{} })},
		{name: "empty values", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values = nil })},
		{name: "unknown output", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Key = "unknown" })},
		{name: "duplicate output", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values = append(value.Values, value.Values[0]) })},
		{name: "schema mismatch", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].SchemaVersion = 2 })},
		{name: "type mismatch", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].DataType = OutputDataTypeFloat32 })},
		{name: "fixed unit mismatch", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Unit = "W" })},
		{name: "missing dynamic unit", descriptors: []OutputDescriptor{mutateOutputDescriptor(validInstant, func(value *OutputDescriptor) { value.Unit = ""; value.DynamicUnit = true })}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Unit = "" })},
		{name: "zero observed", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].ObservedAt = time.Time{} })},
		{name: "published before observed", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.PublishedAt = at.Add(-time.Second) })},
		{name: "instantaneous range", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].PeriodStart = at.Add(-time.Second) })},
		{name: "instantaneous coverage", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { complete := 100.0; value.Values[0].CoveragePercent = &complete })},
		{name: "windowed zero duration", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { value.Values[0].PeriodStart = at })},
		{name: "observed before window end", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { value.Values[0].ObservedAt = at.Add(-time.Second) })},
		{name: "unknown quality", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Quality = "stale" })},
		{name: "good with error", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Error = "error" })},
		{name: "good with issue", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Issues = []OutputIssue{{Code: "source_bad", Message: "bad"}} })},
		{name: "partial without evidence", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { value.Values[0].Quality = OutputQualityPartial })},
		{name: "partial with error", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) {
			value.Values[0].Quality = OutputQualityPartial
			value.Values[0].CoveragePercent = &coverage
			value.Values[0].Error = "error"
		})},
		{name: "bad with value", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Quality = OutputQualityBad; value.Values[0].Error = "bad" })},
		{name: "bad without error", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Quality = OutputQualityBad; value.Values[0].Value = nil })},
		{name: "wrong concrete type", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Value = float32(1) })},
		{name: "nan float", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Value = math.NaN() })},
		{name: "infinite float", descriptors: []OutputDescriptor{validInstant}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) { value.Values[0].Value = math.Inf(1) })},
		{name: "long string", descriptors: []OutputDescriptor{mutateOutputDescriptor(validInstant, func(value *OutputDescriptor) { value.DataType = OutputDataTypeString; value.Unit = "" })}, batch: mutateOutputBatch(validInstantaneousBatch(at), func(value *OutputBatch) {
			value.Values[0].DataType = OutputDataTypeString
			value.Values[0].Unit = ""
			value.Values[0].Value = strings.Repeat("x", maxOutputStringLength+1)
		})},
		{name: "negative coverage", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { invalid := -1.0; value.Values[0].CoveragePercent = &invalid })},
		{name: "nan coverage", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { invalid := math.NaN(); value.Values[0].CoveragePercent = &invalid })},
		{name: "good partial coverage", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { value.Values[0].CoveragePercent = &coverage })},
		{name: "invalid attribute key", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { value.Values[0].Attributes = map[string]string{"Time Zone": "UTC"} })},
		{name: "untrimmed attribute", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) { value.Values[0].Attributes = map[string]string{"timezone": " UTC "} })},
		{name: "invalid issue code", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) {
			value.Values[0].Quality = OutputQualityPartial
			value.Values[0].Issues = []OutputIssue{{Code: "No Coverage", Message: "bad"}}
		})},
		{name: "empty issue message", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) {
			value.Values[0].Quality = OutputQualityPartial
			value.Values[0].Issues = []OutputIssue{{Code: "no_coverage"}}
		})},
		{name: "half issue period", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) {
			value.Values[0].Quality = OutputQualityPartial
			value.Values[0].Issues = []OutputIssue{{Code: "no_coverage", Message: "bad", PeriodStart: &issueStart}}
		})},
		{name: "issue outside output", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) {
			outside := at.Add(time.Minute)
			value.Values[0].Quality = OutputQualityPartial
			value.Values[0].Issues = []OutputIssue{{Code: "no_coverage", Message: "bad", PeriodStart: &issueStart, PeriodEnd: &outside}}
		})},
		{name: "issue reversed", descriptors: []OutputDescriptor{validWindow}, batch: mutateOutputBatch(validWindowedBatch(at), func(value *OutputBatch) {
			value.Values[0].Quality = OutputQualityPartial
			value.Values[0].Issues = []OutputIssue{{Code: "no_coverage", Message: "bad", PeriodStart: &issueEnd, PeriodEnd: &issueStart}}
		})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeOutputBatch(test.batch, test.descriptors); !errors.Is(err, ErrInvalidOutputBatch) {
				t.Fatalf("NormalizeOutputBatch() error = %v, want ErrInvalidOutputBatch", err)
			}
		})
	}
}

func validOutputDescriptor(key OutputKey) OutputDescriptor {
	return OutputDescriptor{
		Key: key, Name: "Metric", Description: "Published metric", SchemaVersion: 1,
		DataType: OutputDataTypeFloat64, Unit: "kW", PeriodKind: OutputPeriodInstantaneous,
	}
}

func validWindowedDescriptor(key OutputKey) OutputDescriptor {
	descriptor := validOutputDescriptor(key)
	descriptor.PeriodKind = OutputPeriodWindowed
	return descriptor
}

func validInstantaneousBatch(at time.Time) OutputBatch {
	return OutputBatch{
		InstanceID: uuid.New(), Sequence: 1, PublishedAt: at.Add(time.Second),
		Values: []OutputValue{{
			Key: "metric", SchemaVersion: 1, DataType: OutputDataTypeFloat64, Unit: "kW",
			Value: 1.5, Quality: OutputQualityGood, ObservedAt: at, PeriodStart: at, PeriodEnd: at,
		}},
	}
}

func validWindowedBatch(at time.Time) OutputBatch {
	batch := validInstantaneousBatch(at)
	batch.Values[0].PeriodStart = at.Add(-time.Hour)
	return batch
}

func mutateOutputDescriptor(value OutputDescriptor, mutate func(*OutputDescriptor)) OutputDescriptor {
	mutate(&value)
	return value
}

func mutateOutputBatch(value OutputBatch, mutate func(*OutputBatch)) OutputBatch {
	value = CloneOutputBatch(value)
	mutate(&value)
	return value
}
