package dataloggerpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

func TestHistoryRepositoryPublishesOnlyNewCommittedBatchesAndRecoversLatest_Integration(t *testing.T) {
	database, tagIDs := newRepositoryDatabase(t)
	broker, err := datalogger.NewCommittedBatchBroker(datalogger.WithCommittedBatchBuffer(4))
	if err != nil {
		t.Fatalf("NewCommittedBatchBroker() error = %v", err)
	}
	history := NewHistoryRepository(database, WithCommittedBatchPublisher(broker))
	feed, err := datalogger.NewCommittedBatchFeed(history, broker)
	if err != nil {
		t.Fatalf("NewCommittedBatchFeed() error = %v", err)
	}
	definitionRepository := NewRepository(database)
	startAt := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Committed Batch Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(context.Background(), &logger, tagIDs); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	subscription, err := feed.Subscribe(logger.ID)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	t.Cleanup(subscription.Close)
	firstAt := startAt.Add(time.Minute)
	first := datalogger.RawBatch{LoggerID: logger.ID, BatchAt: firstAt, Samples: []datalogger.RawSample{
		{TagID: tagIDs[0], ObservedAt: firstAt.Add(-time.Second), DataType: "float64", Value: float64(10.5), Quality: datalogger.RawQualityGood},
		{TagID: tagIDs[1], ObservedAt: firstAt.Add(-time.Second), DataType: "float64", Quality: datalogger.RawQualityBad, Error: "  source unavailable  "},
	}}
	if err := history.WriteBatch(context.Background(), first); err != nil {
		t.Fatalf("WriteBatch(first) error = %v", err)
	}
	published := awaitPostgresCommittedBatch(t, subscription.Events())
	if published.LoggerID != logger.ID || !published.BatchAt.Equal(firstAt) || len(published.Samples) != 2 {
		t.Fatalf("published first batch = %#v", published)
	}
	publishedByTag := make(map[uuid.UUID]datalogger.RawSample, len(published.Samples))
	for _, sample := range published.Samples {
		publishedByTag[sample.TagID] = sample
	}
	if publishedByTag[tagIDs[0]].Quality != datalogger.RawQualityGood || publishedByTag[tagIDs[0]].Value != float64(10.5) {
		t.Errorf("published good sample = %#v", publishedByTag[tagIDs[0]])
	}
	if publishedByTag[tagIDs[1]].Quality != datalogger.RawQualityBad || publishedByTag[tagIDs[1]].Value != nil || publishedByTag[tagIDs[1]].Error != "source unavailable" {
		t.Errorf("published bad sample = %#v", publishedByTag[tagIDs[1]])
	}

	if err := history.WriteBatch(context.Background(), first); err != nil {
		t.Fatalf("WriteBatch(idempotent) error = %v", err)
	}
	assertNoPostgresCommittedBatch(t, subscription.Events())

	invalid := datalogger.RawBatch{LoggerID: logger.ID, BatchAt: firstAt.Add(time.Minute), Samples: []datalogger.RawSample{{TagID: uuid.New(), ObservedAt: firstAt, DataType: "float64", Value: float64(1), Quality: datalogger.RawQualityGood}}}
	if err := history.WriteBatch(context.Background(), invalid); !errors.Is(err, datalogger.ErrRawTagNotSelected) {
		t.Fatalf("WriteBatch(invalid) error = %v", err)
	}
	assertNoPostgresCommittedBatch(t, subscription.Events())
	limit := int64(1024 * 1024)
	rollbackLogger := datalogger.Logger{Name: "Rollback Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, MaxSizeBytes: &limit, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(context.Background(), &rollbackLogger, tagIDs[:1]); err != nil {
		t.Fatalf("Create(rollback logger) error = %v", err)
	}
	if err := database.Exec("ALTER TABLE data_loggers DROP CONSTRAINT data_loggers_max_size_check").Error; err != nil {
		t.Fatalf("dropping storage-limit constraint in isolated test schema: %v", err)
	}
	if err := database.Exec("UPDATE data_loggers SET max_size_bytes = 1 WHERE id = ?", rollbackLogger.ID).Error; err != nil {
		t.Fatalf("forcing rollback storage policy: %v", err)
	}
	rollbackSubscription, err := feed.Subscribe(rollbackLogger.ID)
	if err != nil {
		t.Fatalf("Subscribe(rollback logger) error = %v", err)
	}
	t.Cleanup(rollbackSubscription.Close)
	rolledBack := datalogger.RawBatch{LoggerID: rollbackLogger.ID, BatchAt: firstAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: firstAt, DataType: "float64", Quality: datalogger.RawQualityBad, Error: "rollback fixture"}}}
	if err := history.WriteBatch(context.Background(), rolledBack); !errors.Is(err, datalogger.ErrStorageLimitTooSmall) {
		t.Fatalf("WriteBatch(rolled back) error = %v", err)
	}
	assertNoPostgresCommittedBatch(t, rollbackSubscription.Events())
	if _, err := history.LatestBatch(context.Background(), rollbackLogger.ID); !errors.Is(err, datalogger.ErrRawBatchNotFound) {
		t.Errorf("LatestBatch(rolled back) error = %v", err)
	}

	secondAt := firstAt.Add(2 * time.Minute)
	second := datalogger.RawBatch{LoggerID: logger.ID, BatchAt: secondAt, Samples: []datalogger.RawSample{{TagID: tagIDs[2], ObservedAt: secondAt, DataType: "float64", Value: float64(88.25), Quality: datalogger.RawQualityGood}}}
	if err := history.WriteBatch(context.Background(), second); err != nil {
		t.Fatalf("WriteBatch(second) error = %v", err)
	}
	if event := awaitPostgresCommittedBatch(t, subscription.Events()); !event.BatchAt.Equal(secondAt) || len(event.Samples) != 1 || event.Samples[0].Value != float64(88.25) {
		t.Errorf("published second batch = %#v", event)
	}

	restartedBroker, _ := datalogger.NewCommittedBatchBroker()
	restartedFeed, err := datalogger.NewCommittedBatchFeed(history, restartedBroker)
	if err != nil {
		t.Fatalf("NewCommittedBatchFeed(restart) error = %v", err)
	}
	latest, err := restartedFeed.Latest(context.Background(), logger.ID)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if !latest.BatchAt.Equal(secondAt) || len(latest.Samples) != 1 || latest.Samples[0].Value != float64(88.25) {
		t.Errorf("latest after restart = %#v", latest)
	}
	latest.Samples[0].Value = float64(0)
	again, err := restartedFeed.Latest(context.Background(), logger.ID)
	if err != nil || again.Samples[0].Value != float64(88.25) {
		t.Errorf("latest immutable retry = %#v, error = %v", again, err)
	}

	missingLogger := datalogger.Logger{Name: "Empty Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(context.Background(), &missingLogger, tagIDs[:1]); err != nil {
		t.Fatalf("Create(empty logger) error = %v", err)
	}
	if _, err := restartedFeed.Latest(context.Background(), missingLogger.ID); !errors.Is(err, datalogger.ErrRawBatchNotFound) {
		t.Errorf("Latest(empty logger) error = %v", err)
	}
}

func TestHistoryRepositoryDoesNotLoseCommitWhenPublisherPanics_Integration(t *testing.T) {
	database, tagIDs := newRepositoryDatabase(t)
	history := NewHistoryRepository(database, WithCommittedBatchPublisher(panickingBatchPublisher{}))
	definitionRepository := NewRepository(database)
	batchAt := time.Date(2026, time.August, 22, 1, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Publisher Panic Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: batchAt.Add(-time.Minute), Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(context.Background(), &logger, tagIDs[:1]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	batch := datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: float64(1), Quality: datalogger.RawQualityGood}}}
	if err := history.WriteBatch(context.Background(), batch); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	latest, err := history.LatestBatch(context.Background(), logger.ID)
	if err != nil || latest == nil || !latest.BatchAt.Equal(batchAt) {
		t.Errorf("LatestBatch() = %#v, %v", latest, err)
	}
}

type panickingBatchPublisher struct{}

func (panickingBatchPublisher) PublishCommittedBatch(datalogger.RawBatch) { panic("publisher failed") }

func awaitPostgresCommittedBatch(t *testing.T, events <-chan datalogger.RawBatch) datalogger.RawBatch {
	t.Helper()
	select {
	case batch := <-events:
		return batch
	case <-time.After(time.Second):
		t.Fatal("committed batch was not published")
		return datalogger.RawBatch{}
	}
}

func assertNoPostgresCommittedBatch(t *testing.T, events <-chan datalogger.RawBatch) {
	t.Helper()
	select {
	case batch := <-events:
		t.Fatalf("unexpected committed batch: %#v", batch)
	case <-time.After(20 * time.Millisecond):
	}
}

var _ datalogger.CommittedBatchPublisher = panickingBatchPublisher{}
