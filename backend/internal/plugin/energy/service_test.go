package energy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestEnergyServiceValidatesDependenciesAndQueries(t *testing.T) {
	t.Parallel()
	var nilInstances *energyInstanceReader
	var nilHistory *energyHistoryReader
	if _, err := NewService(nil, &energyHistoryReader{}); !errors.Is(err, ErrInstanceReaderRequired) {
		t.Errorf("NewService(nil instances) error = %v", err)
	}
	if _, err := NewService(nilInstances, &energyHistoryReader{}); !errors.Is(err, ErrInstanceReaderRequired) {
		t.Errorf("NewService(typed nil instances) error = %v", err)
	}
	if _, err := NewService(&energyInstanceReader{}, nil); !errors.Is(err, ErrHistoryReaderRequired) {
		t.Errorf("NewService(nil history) error = %v", err)
	}
	if _, err := NewService(&energyInstanceReader{}, nilHistory); !errors.Is(err, ErrHistoryReaderRequired) {
		t.Errorf("NewService(typed nil history) error = %v", err)
	}
	if _, err := NewService(&energyInstanceReader{}, &energyHistoryReader{}, WithServiceClock(nil)); !errors.Is(err, ErrInvalidHistoryQuery) {
		t.Errorf("NewService(nil clock) error = %v", err)
	}

	service := newEnergyTestService(t, Config{}, nil, nil)
	invalidQueries := []HistoryInput{
		{},
		{From: time.Now(), To: time.Now().Add(time.Hour), Bucket: "2h"},
		{From: time.Now(), To: time.Now().Add(367 * 24 * time.Hour), Bucket: datalogger.QueryBucket1Hour},
		{From: time.Now(), To: time.Now().Add(time.Hour), Bucket: datalogger.QueryBucket1Hour, PerPage: 501},
	}
	for _, input := range invalidQueries {
		if _, err := service.History(context.Background(), uuid.New(), input); !errors.Is(err, ErrInvalidHistoryQuery) {
			t.Errorf("History(%#v) error = %v", input, err)
		}
	}
	if _, err := service.History(nil, uuid.New(), HistoryInput{}); !errors.Is(err, ErrInvalidHistoryQuery) {
		t.Errorf("History(nil context) error = %v", err)
	}
	if err := service.ValidateHistory(context.Background(), uuid.Nil, HistoryInput{}); !errors.Is(err, ErrInvalidHistoryQuery) {
		t.Errorf("ValidateHistory(nil ID) error = %v", err)
	}
	if err := service.ExportCSV(context.Background(), uuid.New(), HistoryInput{}, nil); !errors.Is(err, ErrInvalidHistoryQuery) {
		t.Errorf("ExportCSV(nil writer) error = %v", err)
	}
}

func TestEnergyServiceHistoryPaginatesNewestTimezoneAlignedBuckets(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 7200)
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	batches := []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, start, 0, 0),
		energyRawBatch(loggerID, electricalID, thermalID, start.Add(time.Hour), 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, start.Add(2*time.Hour), 20, 60),
		energyRawBatch(loggerID, electricalID, thermalID, start.Add(3*time.Hour), 30, 90),
	}
	history := &energyHistoryReader{batches: batches}
	service := newEnergyTestServiceWithID(t, instanceID, config, history, nil)
	result, err := service.History(context.Background(), instanceID, HistoryInput{
		From: start, To: start.Add(3 * time.Hour), Bucket: datalogger.QueryBucket1Hour, Page: 1, PerPage: 2,
	})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if result.Total != 3 || result.TotalPages != 2 || result.Page != 1 || result.PerPage != 2 || len(result.Data) != 2 {
		t.Fatalf("History() pagination = %#v", result)
	}
	if !result.Data[0].From.Equal(start.Add(2*time.Hour)) || !result.Data[0].To.Equal(start.Add(3*time.Hour)) || result.Data[0].Electrical.KilowattHours != 25 || result.Data[0].Thermal.KilowattHours != 75 || result.Data[0].COP.Value != 3 {
		t.Errorf("newest bucket = %#v", result.Data[0])
	}
	if !result.Data[1].From.Equal(start.Add(time.Hour)) || result.Data[1].Electrical.KilowattHours != 15 {
		t.Errorf("second bucket = %#v", result.Data[1])
	}
	if history.input.LoggerID != loggerID || !history.input.From.Equal(start.Add(time.Hour)) || !history.input.To.Equal(start.Add(3*time.Hour)) || !history.input.IncludeNeighbors || len(history.input.TagIDs) != 2 {
		t.Errorf("ListBatches() input = %#v", history.input)
	}
	if history.latestCalls != 0 {
		t.Errorf("History() read latest/live seed %d times", history.latestCalls)
	}

	result, err = service.History(context.Background(), instanceID, HistoryInput{
		From: start, To: start.Add(3 * time.Hour), Bucket: datalogger.QueryBucket1Hour, Page: 2, PerPage: 2,
	})
	if err != nil || len(result.Data) != 1 || !result.Data[0].From.Equal(start) || result.Data[0].Electrical.KilowattHours != 5 {
		t.Errorf("History(page 2) = %#v, %v", result, err)
	}
}

func TestEnergyServiceHistoryDownsamplesLongChartRangesButExportKeepsRequestedBucket(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	service := newEnergyTestServiceWithID(t, instanceID, Config{}, &energyHistoryReader{}, nil)
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	input := HistoryInput{From: from, To: from.Add(30 * 24 * time.Hour), Bucket: datalogger.QueryBucket1Minute, Page: 1, PerPage: MaxHistoryPoints}
	result, err := service.History(context.Background(), instanceID, input)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if result.RequestedBucket != datalogger.QueryBucket1Minute || result.Bucket != datalogger.QueryBucket6Hours || !result.Downsampled || result.PointLimit != MaxHistoryPoints || result.Total != 120 || len(result.Data) != 120 {
		t.Fatalf("bounded History() = %#v", result)
	}
	raw, err := service.historyResult(context.Background(), instanceID, input, false)
	if err != nil {
		t.Fatalf("unbounded export history error = %v", err)
	}
	if raw.Bucket != datalogger.QueryBucket1Minute || raw.Downsampled || raw.Total != 30*24*60 || len(raw.Data) != MaxHistoryPoints {
		t.Fatalf("unbounded export history = %#v", raw)
	}
}

func TestEnergyServiceHistoryReadsAndAppliesTariffTag(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID, tariffID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 7200)
	config.Tariff = FlatTariff{Mode: TariffModeTag, Currency: "THB", TagID: tariffID}
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	batches := []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, start, 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, start.Add(time.Hour), 10, 30),
	}
	batches[0].Samples = append(batches[0].Samples, goodEnergySample(tariffID, start, "float64", float64(4)))
	batches[1].Samples = append(batches[1].Samples, goodEnergySample(tariffID, start.Add(time.Hour), "float64", float64(5)))
	history := &energyHistoryReader{batches: batches}
	service := newEnergyTestServiceWithID(t, instanceID, config, history, nil)
	result, err := service.History(context.Background(), instanceID, HistoryInput{From: start, To: start.Add(time.Hour), Bucket: datalogger.QueryBucket1Hour})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if result.TariffMode != TariffModeTag || result.TariffTagID != tariffID || len(result.Data) != 1 || !result.Data[0].Cost.Valid || result.Data[0].Cost.Value != 40 {
		t.Errorf("dynamic tariff history = %#v", result)
	}
	if len(history.input.TagIDs) != 3 || history.input.TagIDs[2] != tariffID {
		t.Errorf("ListBatches() Tag IDs = %#v", history.input.TagIDs)
	}
}

func TestEnergyServiceCalendarBucketsHonorDST(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	from := time.Date(2026, time.November, 1, 0, 0, 0, 0, location)
	to := time.Date(2026, time.November, 2, 0, 0, 0, 0, location)
	windows, total := pagedBucketWindows(from.UTC(), to.UTC(), datalogger.QueryBucket1Day, location, 1, 10)
	if total != 1 || len(windows) != 1 || windows[0].To.Sub(windows[0].From) != 25*time.Hour {
		t.Errorf("fall-back daily windows = %#v, total %d", windows, total)
	}
	springFrom := time.Date(2026, time.March, 8, 0, 0, 0, 0, location)
	springTo := time.Date(2026, time.March, 9, 0, 0, 0, 0, location)
	windows, total = pagedBucketWindows(springFrom.UTC(), springTo.UTC(), datalogger.QueryBucket1Day, location, 1, 10)
	if total != 1 || len(windows) != 1 || windows[0].To.Sub(windows[0].From) != 23*time.Hour {
		t.Errorf("spring-forward daily windows = %#v, total %d", windows, total)
	}
}

func TestEnergyServiceFixedBucketsAlignBeforeUnixEpoch(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	from := time.Date(1960, time.January, 2, 0, 30, 0, 0, location)
	to := from.Add(2 * time.Hour)
	windows, total := pagedBucketWindows(from.UTC(), to.UTC(), datalogger.QueryBucket1Hour, location, 1, 10)
	if total != 3 || len(windows) != 3 {
		t.Fatalf("fixed windows = %#v, total %d", windows, total)
	}
	if windows[0].From.In(location).Hour() != 2 || windows[0].To.In(location).Hour() != 2 || windows[2].From.In(location).Minute() != 30 {
		t.Errorf("fixed local alignment = %#v", windows)
	}
}

func TestEnergyServiceSelectsOnlyMetricsOverlappingEachPeriod(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	metrics := make([]BatchMetrics, 6)
	for index := range metrics {
		metrics[index].BatchAt = start.Add(time.Duration(index-1) * time.Hour)
	}
	tests := []struct {
		name       string
		from       time.Time
		to         time.Time
		wantStart  time.Time
		wantEnd    time.Time
		wantLength int
	}{
		{name: "exact boundaries", from: start.Add(time.Hour), to: start.Add(2 * time.Hour), wantStart: start.Add(time.Hour), wantEnd: start.Add(2 * time.Hour), wantLength: 2},
		{name: "clipped boundaries", from: start.Add(90 * time.Minute), to: start.Add(150 * time.Minute), wantStart: start.Add(time.Hour), wantEnd: start.Add(3 * time.Hour), wantLength: 3},
		{name: "before history", from: start.Add(-2 * time.Hour), to: start.Add(-90 * time.Minute), wantStart: start.Add(-time.Hour), wantEnd: start.Add(-time.Hour), wantLength: 1},
		{name: "after history", from: start.Add(5 * time.Hour), to: start.Add(6 * time.Hour), wantStart: start.Add(4 * time.Hour), wantEnd: start.Add(4 * time.Hour), wantLength: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected := metricsForPeriod(metrics, test.from, test.to)
			if len(selected) != test.wantLength || !selected[0].BatchAt.Equal(test.wantStart) || !selected[len(selected)-1].BatchAt.Equal(test.wantEnd) {
				t.Errorf("metricsForPeriod() = %#v", selected)
			}
		})
	}
}

func TestEnergyServiceBoundsRawHistoryReadGroups(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, time.August, 22, 0, 0, 0, 0, time.UTC)
	windows := make([]bucketWindow, 366)
	for index := range windows {
		from := start.Add(time.Duration(365-index) * 24 * time.Hour)
		windows[index] = bucketWindow{From: from, To: from.Add(24 * time.Hour)}
	}
	groups := groupHistoryWindows(windows, maxRawHistoryReadSpan)
	if len(groups) != 12 {
		t.Fatalf("groupHistoryWindows() groups = %d, want 12", len(groups))
	}
	count := 0
	for groupIndex, group := range groups {
		count += len(group)
		if span := group[0].To.Sub(group[len(group)-1].From); span > maxRawHistoryReadSpan {
			t.Errorf("group %d span = %s", groupIndex, span)
		}
		if groupIndex > 0 && !groups[groupIndex-1][len(groups[groupIndex-1])-1].From.Equal(group[0].To) {
			t.Errorf("groups %d and %d are not contiguous", groupIndex-1, groupIndex)
		}
	}
	if count != len(windows) {
		t.Errorf("grouped windows = %d, want %d", count, len(windows))
	}
}

func TestEnergyServiceOverviewUsesLatestAndCalendarPeriods(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 31*24*60*60)
	config.Timezone = "Asia/Bangkok"
	now := time.Date(2026, time.August, 23, 5, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.FixedZone("ICT", 7*60*60)).UTC()
	batches := []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, monthStart, 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, now, 10, 30),
	}
	history := &energyHistoryReader{batches: batches, latest: &batches[1]}
	service := newEnergyTestServiceWithID(t, instanceID, config, history, func() time.Time { return now })
	result, err := service.Overview(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result.Latest == nil || result.Latest.Electrical.Kilowatts != 10 || result.Latest.COP.Value != 3 || !result.AsOf.Equal(now) {
		t.Errorf("Overview latest = %#v", result)
	}
	if !history.input.From.Equal(monthStart) || !history.input.To.Equal(now) || !history.input.IncludeNeighbors {
		t.Errorf("Overview ListBatches() input = %#v", history.input)
	}
	if result.Month.Electrical.CoveragePercent != 100 || result.Month.Electrical.KilowattHours != now.Sub(monthStart).Hours()*10 {
		t.Errorf("month metrics = %#v", result.Month)
	}
	if result.Today.From.In(time.FixedZone("ICT", 7*60*60)).Hour() != 0 {
		t.Errorf("today boundary = %s", result.Today.From)
	}
}

func TestEnergyServiceExportsFullRangeAndVisibleIssues(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 120)
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	history := &energyHistoryReader{batches: []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, start, 10, 30),
		{LoggerID: loggerID, BatchAt: start.Add(time.Minute), Samples: []datalogger.RawSample{
			{TagID: electricalID, ObservedAt: start.Add(time.Minute), DataType: "float64", Quality: datalogger.RawQualityBad, Error: "Modbus 0x02 Illegal Data Address"},
			goodEnergySample(thermalID, start.Add(time.Minute), "float64", float64(30)),
		}},
	}}
	service := newEnergyTestServiceWithID(t, instanceID, config, history, nil)
	var output bytes.Buffer
	err := service.ExportCSV(context.Background(), instanceID, HistoryInput{From: start, To: start.Add(501 * time.Minute), Bucket: datalogger.QueryBucket1Minute}, &output)
	if err != nil {
		t.Fatalf("ExportCSV() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 502 {
		t.Fatalf("CSV lines = %d, want 502", len(lines))
	}
	if !strings.Contains(output.String(), "Modbus 0x02 Illegal Data Address") || !strings.Contains(lines[0], "electrical_coverage_percent") {
		t.Errorf("CSV = %q", output.String()[:min(output.Len(), 1000)])
	}
	if history.calls != 2 {
		t.Errorf("ListBatches() calls = %d, want 2 paginated reads", history.calls)
	}
}

func TestEnergyServiceClassifiesInstancesAndPropagatesReaders(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	readerError := errors.New("database unavailable")
	service, _ := NewService(&energyInstanceReader{err: readerError}, &energyHistoryReader{})
	input := HistoryInput{From: time.Now().Add(-time.Hour), To: time.Now(), Bucket: datalogger.QueryBucket1Hour}
	if _, err := service.History(context.Background(), instanceID, input); !errors.Is(err, readerError) {
		t.Errorf("History(instance error) = %v", err)
	}
	service, _ = NewService(&energyInstanceReader{instance: &plugin.Instance{ID: instanceID, Type: "other", Config: plugin.Config(`{}`)}}, &energyHistoryReader{})
	if _, err := service.History(context.Background(), instanceID, input); !errors.Is(err, ErrEnergyInstanceRequired) {
		t.Errorf("History(wrong type) = %v", err)
	}
}

func TestEnergyServiceSeedsAndResumesLiveMetricsFromPersistedLatest(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 60)
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	latest := energyRawBatch(loggerID, electricalID, thermalID, start, 2, 6)
	history := &energyHistoryReader{latest: &latest}
	hub, _ := NewLiveHub()
	instance := &plugin.Instance{ID: instanceID, Type: PluginType, Config: encodeEnergyConfig(t, config)}
	service, err := NewService(&energyInstanceReader{instance: instance}, history, WithLiveHub(hub))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	subscription, err := service.SubscribeLive(context.Background(), instanceID, "")
	if err != nil {
		t.Fatalf("SubscribeLive() error = %v", err)
	}
	if len(subscription.Replay) != 1 || subscription.Replay[0].Metrics.Electrical.Kilowatts != 2 || subscription.Replay[0].Metrics.COP.Value != 3 || history.latestCalls != 1 || history.calls != 0 {
		t.Fatalf("persisted replay = %#v, latest calls %d", subscription.Replay, history.latestCalls)
	}
	cursor := subscription.Replay[0].ID
	subscription.Unsubscribe()
	hub.Publish(instanceID, validBatchMetrics(start.Add(time.Second), 3, 12))
	resumed, err := service.SubscribeLive(context.Background(), instanceID, cursor)
	if err != nil {
		t.Fatalf("SubscribeLive(resume) error = %v", err)
	}
	defer resumed.Unsubscribe()
	if resumed.Reset || len(resumed.Replay) != 1 || resumed.Replay[0].Sequence != 2 || resumed.Replay[0].Metrics.COP.Value != 4 {
		t.Errorf("resumed replay = %#v", resumed)
	}
	latestCalls := history.latestCalls
	if _, err := service.SubscribeLive(context.Background(), instanceID, "invalid"); !errors.Is(err, ErrInvalidLiveCursor) || history.latestCalls != latestCalls {
		t.Errorf("SubscribeLive(invalid cursor) = %v, latest calls %d", err, history.latestCalls)
	}
	serviceWithoutHub, _ := NewService(&energyInstanceReader{instance: instance}, history)
	if _, err := serviceWithoutHub.SubscribeLive(context.Background(), instanceID, ""); !errors.Is(err, ErrLiveHubRequired) {
		t.Errorf("SubscribeLive(no hub) error = %v", err)
	}
}

func TestEnergyServiceResetBuildsRetentionIndependentArchive(t *testing.T) {
	loggerID, electricalID, thermalID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 120)
	start := time.Date(2026, time.September, 21, 1, 0, 0, 0, time.UTC)
	batches := []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, start, 6, 18),
		energyRawBatch(loggerID, electricalID, thermalID, start.Add(time.Minute), 6, 18),
		energyRawBatch(loggerID, electricalID, thermalID, start.Add(2*time.Minute), 6, 18),
	}
	history := &energyHistoryReader{batches: batches, latest: &batches[2]}
	run := &MeasurementRun{ID: uuid.New(), PluginInstanceID: instanceID, Name: "Point A", Status: MeasurementRunActive, StartedAt: start, ConfigVersion: 1, ConfigSnapshot: encodeEnergyConfig(t, config)}
	runs := &energyRunRepository{active: run}
	instance := &plugin.Instance{ID: instanceID, Type: PluginType, Name: "Portable Energy", ConfigVersion: 1, Config: encodeEnergyConfig(t, config)}
	service, err := NewService(&energyInstanceReader{instance: instance}, history, WithMeasurementRuns(runs))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	next, err := service.ResetRun(context.Background(), instanceID, ResetRunInput{ExpectedRunID: run.ID, Name: "Point B", Reason: "moved"})
	if err != nil {
		t.Fatalf("ResetRun() error = %v", err)
	}
	if next.Name != "Point B" || runs.reset.ArchiveSHA256 == "" {
		t.Fatalf("next/reset = %+v / %+v", next, runs.reset)
	}
	var archive MeasurementRunArchive
	if err := json.Unmarshal(runs.reset.ArchivePayload, &archive); err != nil {
		t.Fatalf("decoding archive: %v", err)
	}
	if archive.RunID != run.ID || archive.Bucket != "1m" || len(archive.Series) != 2 || archive.Summary.Electrical.KilowattHours != 0.2 {
		t.Errorf("archive = %+v", archive)
	}
	digest := sha256.Sum256(runs.reset.ArchivePayload)
	if hex.EncodeToString(digest[:]) != runs.reset.ArchiveSHA256 {
		t.Error("archive checksum does not match canonical payload")
	}

	endedAt, archivedAt, checksum := batches[2].BatchAt, batches[2].BatchAt, runs.reset.ArchiveSHA256
	runs.archived = &MeasurementRun{ID: run.ID, PluginInstanceID: instanceID, Name: run.Name, Status: MeasurementRunArchived, StartedAt: start, EndedAt: &endedAt, ConfigVersion: 1, ConfigSnapshot: run.ConfigSnapshot, ArchivePayload: runs.reset.ArchivePayload, ArchiveSHA256: &checksum, ArchivedAt: &archivedAt}
	var output bytes.Buffer
	if err := service.ExportArchivedRunCSV(context.Background(), instanceID, run.ID, &output); err != nil {
		t.Fatalf("ExportArchivedRunCSV() error = %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(output.String()), "\n"); len(lines) != 3 || !strings.Contains(lines[0], "run_id") {
		t.Errorf("archive CSV = %q", output.String())
	}
}

type energyInstanceReader struct {
	instance *plugin.Instance
	err      error
}

func (reader *energyInstanceReader) Find(context.Context, uuid.UUID) (*plugin.Instance, error) {
	return reader.instance, reader.err
}

type energyHistoryReader struct {
	input       datalogger.RawBatchListInput
	batches     []datalogger.RawBatch
	latest      *datalogger.RawBatch
	latestErr   error
	latestCalls int
	err         error
	calls       int
}

type energyRunRepository struct {
	active   *MeasurementRun
	archived *MeasurementRun
	reset    ResetMeasurementRunInput
}

func (repository *energyRunRepository) FindActive(context.Context, uuid.UUID) (*MeasurementRun, error) {
	if repository.active == nil {
		return nil, ErrMeasurementRunNotFound
	}
	return repository.active, nil
}
func (repository *energyRunRepository) FindArchived(context.Context, uuid.UUID, uuid.UUID) (*MeasurementRun, error) {
	if repository.archived == nil {
		return nil, ErrMeasurementRunNotFound
	}
	return repository.archived, nil
}
func (repository *energyRunRepository) ListArchived(context.Context, uuid.UUID) ([]MeasurementRun, error) {
	if repository.archived == nil {
		return []MeasurementRun{}, nil
	}
	return []MeasurementRun{*repository.archived}, nil
}
func (repository *energyRunRepository) Start(_ context.Context, input StartMeasurementRunInput) (*MeasurementRun, error) {
	return &MeasurementRun{ID: uuid.New(), PluginInstanceID: input.PluginInstanceID, Name: input.Name, Status: MeasurementRunActive, StartedAt: input.StartedAt, ConfigVersion: input.ConfigVersion, ConfigSnapshot: input.ConfigSnapshot}, nil
}
func (repository *energyRunRepository) ArchiveAndStart(_ context.Context, input ResetMeasurementRunInput) (*MeasurementRun, error) {
	repository.reset = input
	return &MeasurementRun{ID: uuid.New(), PluginInstanceID: input.PluginInstanceID, Name: input.Name, Reason: input.Reason, Status: MeasurementRunActive, StartedAt: input.Cutoff, ConfigVersion: input.ConfigVersion, ConfigSnapshot: input.ConfigSnapshot}, nil
}

func (reader *energyHistoryReader) LatestBatch(context.Context, uuid.UUID) (*datalogger.RawBatch, error) {
	reader.latestCalls++
	if reader.latest == nil {
		if reader.latestErr != nil {
			return nil, reader.latestErr
		}
		return nil, datalogger.ErrRawBatchNotFound
	}
	value := *reader.latest
	value.Samples = append([]datalogger.RawSample(nil), value.Samples...)
	return &value, reader.latestErr
}

func (reader *energyHistoryReader) ListBatches(_ context.Context, input datalogger.RawBatchListInput) ([]datalogger.RawBatch, error) {
	reader.input = input
	reader.calls++
	result := make([]datalogger.RawBatch, len(reader.batches))
	for index, batch := range reader.batches {
		result[index] = batch
		result[index].Samples = append([]datalogger.RawSample(nil), batch.Samples...)
	}
	return result, reader.err
}

func newEnergyTestService(t *testing.T, config Config, history *energyHistoryReader, clock func() time.Time) *Service {
	t.Helper()
	return newEnergyTestServiceWithID(t, uuid.New(), config, history, clock)
}

func newEnergyTestServiceWithID(t *testing.T, instanceID uuid.UUID, config Config, history *energyHistoryReader, clock func() time.Time) *Service {
	t.Helper()
	if config.LoggerID == uuid.Nil {
		config = testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 60)
	}
	if history == nil {
		history = &energyHistoryReader{}
	}
	instance := &plugin.Instance{ID: instanceID, Type: PluginType, Config: encodeEnergyConfig(t, config)}
	options := []ServiceOption{}
	if clock != nil {
		options = append(options, WithServiceClock(clock))
	}
	service, err := NewService(&energyInstanceReader{instance: instance}, history, options...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

var _ InstanceReader = (*energyInstanceReader)(nil)
var _ HistoryReader = (*energyHistoryReader)(nil)
