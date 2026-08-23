package publisher

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxSourceSelections = 256

var (
	ErrInvalidSourceReference = errors.New("invalid Publisher source reference")
	ErrInvalidSourceSelection = errors.New("invalid Publisher source selection")
	ErrDuplicateSourceAlias   = errors.New("duplicate Publisher source alias")
	ErrDuplicateSource        = errors.New("duplicate Publisher source")
	ErrSourceNotFound         = errors.New("Publisher source not found")
	ErrSourceSubscriberLag    = errors.New("Publisher source subscriber lagged")
	ErrSourceUpstreamClosed   = errors.New("Publisher source upstream closed")
)

var (
	sourceAliasPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	outputKeyPattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type SourceKind string
type SourceDataType string
type SourcePeriodKind string
type SourceQuality string
type SourceBatchKind string

const (
	SourceKindTag          SourceKind = "tag"
	SourceKindPluginOutput SourceKind = "plugin_output"
)

const (
	SourceDataTypeBool    SourceDataType = "bool"
	SourceDataTypeInt16   SourceDataType = "int16"
	SourceDataTypeUInt16  SourceDataType = "uint16"
	SourceDataTypeInt32   SourceDataType = "int32"
	SourceDataTypeUInt32  SourceDataType = "uint32"
	SourceDataTypeFloat32 SourceDataType = "float32"
	SourceDataTypeFloat64 SourceDataType = "float64"
	SourceDataTypeString  SourceDataType = "string"
)

const (
	SourcePeriodInstantaneous SourcePeriodKind = "instantaneous"
	SourcePeriodWindowed      SourcePeriodKind = "windowed"
)

const (
	SourceQualityGood        SourceQuality = "good"
	SourceQualityPartial     SourceQuality = "partial"
	SourceQualityBad         SourceQuality = "bad"
	SourceQualityUnavailable SourceQuality = "unavailable"
)

const SourceBatchPluginOutput SourceBatchKind = "plugin_output_batch"

type SourceReference struct {
	Kind             SourceKind `json:"kind"`
	TagID            uuid.UUID  `json:"tag_id,omitempty"`
	PluginInstanceID uuid.UUID  `json:"plugin_instance_id,omitempty"`
	OutputKey        string     `json:"output_key,omitempty"`
}

func TagSource(tagID uuid.UUID) SourceReference {
	return SourceReference{Kind: SourceKindTag, TagID: tagID}
}

func PluginOutputSource(instanceID uuid.UUID, outputKey string) SourceReference {
	return SourceReference{Kind: SourceKindPluginOutput, PluginInstanceID: instanceID, OutputKey: outputKey}
}

func (reference SourceReference) Validate() error {
	switch reference.Kind {
	case SourceKindTag:
		if reference.TagID == uuid.Nil || reference.PluginInstanceID != uuid.Nil || reference.OutputKey != "" {
			return ErrInvalidSourceReference
		}
	case SourceKindPluginOutput:
		if reference.TagID != uuid.Nil || reference.PluginInstanceID == uuid.Nil || !outputKeyPattern.MatchString(reference.OutputKey) {
			return ErrInvalidSourceReference
		}
	default:
		return ErrInvalidSourceReference
	}
	return nil
}

func (reference SourceReference) MarshalJSON() ([]byte, error) {
	if err := reference.Validate(); err != nil {
		return nil, err
	}
	switch reference.Kind {
	case SourceKindTag:
		return json.Marshal(struct {
			Kind  SourceKind `json:"kind"`
			TagID uuid.UUID  `json:"tag_id"`
		}{Kind: reference.Kind, TagID: reference.TagID})
	case SourceKindPluginOutput:
		return json.Marshal(struct {
			Kind             SourceKind `json:"kind"`
			PluginInstanceID uuid.UUID  `json:"plugin_instance_id"`
			OutputKey        string     `json:"output_key"`
		}{Kind: reference.Kind, PluginInstanceID: reference.PluginInstanceID, OutputKey: reference.OutputKey})
	default:
		return nil, ErrInvalidSourceReference
	}
}

func (reference *SourceReference) UnmarshalJSON(data []byte) error {
	if reference == nil {
		return ErrInvalidSourceReference
	}
	var decoded struct {
		Kind             SourceKind `json:"kind"`
		TagID            *uuid.UUID `json:"tag_id"`
		PluginInstanceID *uuid.UUID `json:"plugin_instance_id"`
		OutputKey        *string    `json:"output_key"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSourceReference, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidSourceReference
	}
	var candidate SourceReference
	switch decoded.Kind {
	case SourceKindTag:
		if decoded.TagID == nil || decoded.PluginInstanceID != nil || decoded.OutputKey != nil {
			return ErrInvalidSourceReference
		}
		candidate = TagSource(*decoded.TagID)
	case SourceKindPluginOutput:
		if decoded.TagID != nil || decoded.PluginInstanceID == nil || decoded.OutputKey == nil {
			return ErrInvalidSourceReference
		}
		candidate = PluginOutputSource(*decoded.PluginInstanceID, *decoded.OutputKey)
	default:
		return ErrInvalidSourceReference
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*reference = candidate
	return nil
}

func (reference SourceReference) String() string {
	switch reference.Kind {
	case SourceKindTag:
		return "tag:" + reference.TagID.String()
	case SourceKindPluginOutput:
		return "plugin_output:" + reference.PluginInstanceID.String() + ":" + reference.OutputKey
	default:
		return "invalid"
	}
}

type SourceSelection struct {
	Alias     string          `json:"alias"`
	Reference SourceReference `json:"reference"`
}

func NormalizeSourceSelections(selections []SourceSelection) ([]SourceSelection, error) {
	if len(selections) == 0 || len(selections) > MaxSourceSelections {
		return nil, ErrInvalidSourceSelection
	}
	normalized := make([]SourceSelection, len(selections))
	aliases := make(map[string]struct{}, len(selections))
	references := make(map[SourceReference]struct{}, len(selections))
	for index, selection := range selections {
		if selection.Alias != strings.TrimSpace(selection.Alias) || !sourceAliasPattern.MatchString(selection.Alias) {
			return nil, fmt.Errorf("%w: alias %q", ErrInvalidSourceSelection, selection.Alias)
		}
		if err := selection.Reference.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidSourceSelection, err)
		}
		if _, exists := aliases[selection.Alias]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateSourceAlias, selection.Alias)
		}
		if _, exists := references[selection.Reference]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateSource, selection.Reference)
		}
		aliases[selection.Alias] = struct{}{}
		references[selection.Reference] = struct{}{}
		normalized[index] = selection
	}
	return normalized, nil
}

type SourceDescriptor struct {
	Reference     SourceReference  `json:"reference"`
	Name          string           `json:"name"`
	OwnerName     string           `json:"owner_name,omitempty"`
	Description   string           `json:"description,omitempty"`
	SchemaVersion uint             `json:"schema_version"`
	DataType      SourceDataType   `json:"data_type"`
	Unit          string           `json:"unit,omitempty"`
	DynamicUnit   bool             `json:"dynamic_unit,omitempty"`
	PeriodKind    SourcePeriodKind `json:"period_kind"`
	Enabled       bool             `json:"enabled"`
}

type SourceCurrent struct {
	Quality    SourceQuality `json:"quality"`
	Sequence   uint64        `json:"sequence,omitempty"`
	ObservedAt *time.Time    `json:"observed_at,omitempty"`
}

type SourceCatalogEntry struct {
	Descriptor SourceDescriptor `json:"descriptor"`
	Current    SourceCurrent    `json:"current"`
}

type SourceCatalogInput struct {
	Kind    *SourceKind
	Enabled *bool
	Search  string
}

type ResolvedSource struct {
	Alias      string           `json:"alias"`
	Descriptor SourceDescriptor `json:"descriptor"`
}

type SourceBatchIdentity struct {
	Kind     SourceBatchKind `json:"kind"`
	ID       string          `json:"id"`
	Sequence uint64          `json:"sequence"`
}

type SourceIssue struct {
	Code        string     `json:"code"`
	Message     string     `json:"message"`
	Source      string     `json:"source,omitempty"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
}

type SourceSample struct {
	Alias           string               `json:"alias"`
	Reference       SourceReference      `json:"reference"`
	Available       bool                 `json:"available"`
	Sequence        uint64               `json:"sequence,omitempty"`
	SchemaVersion   uint                 `json:"schema_version"`
	DataType        SourceDataType       `json:"data_type"`
	Unit            string               `json:"unit,omitempty"`
	Value           any                  `json:"value"`
	Quality         SourceQuality        `json:"quality"`
	Error           string               `json:"error,omitempty"`
	ObservedAt      *time.Time           `json:"observed_at,omitempty"`
	EmittedAt       *time.Time           `json:"emitted_at,omitempty"`
	PeriodStart     *time.Time           `json:"period_start,omitempty"`
	PeriodEnd       *time.Time           `json:"period_end,omitempty"`
	CoveragePercent *float64             `json:"coverage_percent,omitempty"`
	Issues          []SourceIssue        `json:"issues,omitempty"`
	Attributes      map[string]string    `json:"attributes,omitempty"`
	Batch           *SourceBatchIdentity `json:"batch,omitempty"`
}

type SourceSnapshot struct {
	CapturedAt time.Time      `json:"captured_at"`
	Samples    []SourceSample `json:"samples"`
}

func cloneSourceSnapshot(snapshot SourceSnapshot) SourceSnapshot {
	cloned := SourceSnapshot{CapturedAt: snapshot.CapturedAt, Samples: make([]SourceSample, len(snapshot.Samples))}
	for index, sample := range snapshot.Samples {
		cloned.Samples[index] = cloneSourceSample(sample)
	}
	return cloned
}

func cloneSourceDescriptor(descriptor SourceDescriptor) SourceDescriptor {
	return descriptor
}

func cloneResolvedSources(sources []ResolvedSource) []ResolvedSource {
	cloned := make([]ResolvedSource, len(sources))
	for index, source := range sources {
		cloned[index] = source
		cloned[index].Descriptor = cloneSourceDescriptor(source.Descriptor)
	}
	return cloned
}

func cloneSourceSample(sample SourceSample) SourceSample {
	cloned := sample
	cloned.ObservedAt = cloneTime(sample.ObservedAt)
	cloned.EmittedAt = cloneTime(sample.EmittedAt)
	cloned.PeriodStart = cloneTime(sample.PeriodStart)
	cloned.PeriodEnd = cloneTime(sample.PeriodEnd)
	if sample.CoveragePercent != nil {
		coverage := *sample.CoveragePercent
		cloned.CoveragePercent = &coverage
	}
	cloned.Issues = make([]SourceIssue, len(sample.Issues))
	for index, issue := range sample.Issues {
		cloned.Issues[index] = issue
		cloned.Issues[index].PeriodStart = cloneTime(issue.PeriodStart)
		cloned.Issues[index].PeriodEnd = cloneTime(issue.PeriodEnd)
	}
	if sample.Attributes != nil {
		cloned.Attributes = make(map[string]string, len(sample.Attributes))
		for key, value := range sample.Attributes {
			cloned.Attributes[key] = value
		}
	}
	if sample.Batch != nil {
		batch := *sample.Batch
		cloned.Batch = &batch
	}
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sortCatalog(entries []SourceCatalogEntry) {
	sort.Slice(entries, func(first, second int) bool {
		left := entries[first].Descriptor
		right := entries[second].Descriptor
		if left.Reference.Kind != right.Reference.Kind {
			return left.Reference.Kind == SourceKindTag
		}
		if left.OwnerName != right.OwnerName {
			return left.OwnerName < right.OwnerName
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.Reference.String() < right.Reference.String()
	})
}
