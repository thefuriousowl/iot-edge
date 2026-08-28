package live

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

var (
	ErrUnifiedSourceReaderRequired = errors.New("Asset unified source reader is required")
	ErrTagHistoryRequired          = errors.New("Asset Tag history reader is required")
)

type UnifiedSourceReader interface {
	Snapshot(context.Context, []publisher.SourceSelection) (publisher.SourceSnapshot, error)
	Subscribe(context.Context, []publisher.SourceSelection) (publisher.SourceSubscription, error)
}

type TagHistory interface {
	History(uuid.UUID, int) []tag.TagValue
}

type Reader struct {
	sources UnifiedSourceReader
	tags    TagHistory
}

func NewReader(sources UnifiedSourceReader, tags TagHistory) (*Reader, error) {
	if isNil(sources) {
		return nil, ErrUnifiedSourceReaderRequired
	}
	if isNil(tags) {
		return nil, ErrTagHistoryRequired
	}
	return &Reader{sources: sources, tags: tags}, nil
}

func (reader *Reader) Snapshot(ctx context.Context, references []asset.SourceReference) (asset.LiveSnapshot, error) {
	selections, err := selections(references)
	if err != nil {
		return asset.LiveSnapshot{}, err
	}
	snapshot, err := reader.sources.Snapshot(ctx, selections)
	if err != nil {
		return asset.LiveSnapshot{}, err
	}
	result := asset.LiveSnapshot{CapturedAt: snapshot.CapturedAt, Samples: make([]asset.LiveSample, len(snapshot.Samples))}
	for index, sample := range snapshot.Samples {
		result.Samples[index] = mapSample(sample)
	}
	return result, nil
}

func (reader *Reader) History(reference asset.SourceReference, limit int) []asset.LiveSample {
	if reference.Kind != asset.SourceTag {
		return []asset.LiveSample{}
	}
	values := reader.tags.History(reference.TagID, limit)
	result := make([]asset.LiveSample, len(values))
	for index, value := range values {
		result[index] = mapTagValue(value)
	}
	return result
}

func (reader *Reader) Subscribe(ctx context.Context, references []asset.SourceReference) (asset.LiveSubscription, error) {
	values, err := selections(references)
	if err != nil {
		return nil, err
	}
	upstream, err := reader.sources.Subscribe(ctx, values)
	if err != nil {
		return nil, err
	}
	subscription := &subscription{upstream: upstream, events: make(chan asset.LiveSample, len(references)+16), done: make(chan struct{})}
	go subscription.run()
	return subscription, nil
}

type subscription struct {
	upstream publisher.SourceSubscription
	events   chan asset.LiveSample
	done     chan struct{}
}

func (subscription *subscription) Events() <-chan asset.LiveSample { return subscription.events }
func (subscription *subscription) Err() error                      { return subscription.upstream.Err() }
func (subscription *subscription) Close() {
	select {
	case <-subscription.done:
	default:
		close(subscription.done)
		subscription.upstream.Close()
	}
}
func (subscription *subscription) run() {
	defer close(subscription.events)
	defer subscription.upstream.Close()
	for {
		select {
		case <-subscription.done:
			return
		case sample, open := <-subscription.upstream.Events():
			if !open {
				return
			}
			select {
			case subscription.events <- mapSample(sample):
			case <-subscription.done:
				return
			}
		}
	}
}

func selections(references []asset.SourceReference) ([]publisher.SourceSelection, error) {
	values := make([]publisher.SourceSelection, len(references))
	for index, reference := range references {
		converted, err := publisherReference(reference)
		if err != nil {
			return nil, err
		}
		values[index] = publisher.SourceSelection{Alias: "asset_" + strings.ReplaceAll(reference.Key(), ":", "_"), Reference: converted}
	}
	return values, nil
}

func publisherReference(reference asset.SourceReference) (publisher.SourceReference, error) {
	if err := reference.Validate(); err != nil {
		return publisher.SourceReference{}, asset.ErrInvalidBinding
	}
	if reference.Kind == asset.SourceTag {
		return publisher.TagSource(reference.TagID), nil
	}
	return publisher.PluginOutputSource(reference.PluginInstanceID, reference.OutputKey), nil
}

func assetReference(reference publisher.SourceReference) asset.SourceReference {
	if reference.Kind == publisher.SourceKindTag {
		return asset.TagSource(reference.TagID)
	}
	return asset.PluginOutputSource(reference.PluginInstanceID, reference.OutputKey)
}

func mapSample(sample publisher.SourceSample) asset.LiveSample {
	return asset.LiveSample{Source: assetReference(sample.Reference), Available: sample.Available, Sequence: sample.Sequence, Value: sample.Value, Quality: string(sample.Quality), Error: sanitizeError(sample.Error), ObservedAt: cloneTime(sample.ObservedAt), EmittedAt: cloneTime(sample.EmittedAt), PeriodStart: cloneTime(sample.PeriodStart), PeriodEnd: cloneTime(sample.PeriodEnd)}
}

func mapTagValue(value tag.TagValue) asset.LiveSample {
	observedAt, emittedAt := value.ObservedAt, value.StoredAt
	return asset.LiveSample{Source: asset.TagSource(value.TagID), Available: true, Sequence: value.Sequence, Value: value.Value, Quality: value.Quality, Error: sanitizeError(value.Error), ObservedAt: &observedAt, EmittedAt: &emittedAt, PeriodStart: &observedAt, PeriodEnd: &observedAt}
}

func sanitizeError(value string) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character < 0x20 && character != '\t' {
			return ' '
		}
		return character
	}, value))
	characters := []rune(value)
	if len(characters) > 500 {
		value = string(characters[:500])
	}
	return value
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ asset.LiveSourceReader = (*Reader)(nil)
