package tagsnapshot

import (
	"errors"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

var ErrStoreRequired = errors.New("Tag value snapshot store is required")

type Store interface {
	Snapshot([]uuid.UUID) tag.ValueSnapshot
}

type Reader struct{ store Store }

func NewReader(store Store) (*Reader, error) {
	if store == nil {
		return nil, ErrStoreRequired
	}
	return &Reader{store: store}, nil
}

func (reader *Reader) Snapshot(tagIDs []uuid.UUID) map[uuid.UUID]datalogger.SnapshotValue {
	snapshot := reader.store.Snapshot(tagIDs)
	values := make(map[uuid.UUID]datalogger.SnapshotValue, len(snapshot))
	for tagID, value := range snapshot {
		values[tagID] = datalogger.SnapshotValue{
			ObservedAt: value.ObservedAt,
			DataType:   string(value.DataType),
			Value:      value.Value,
			Quality:    value.Quality,
			Error:      value.Error,
		}
	}
	return values
}

var _ datalogger.SnapshotReader = (*Reader)(nil)
