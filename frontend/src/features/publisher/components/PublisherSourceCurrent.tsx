import type { PublisherSourceCatalogEntry } from "../../../types/publisher";

interface PublisherSourceCurrentProps {
  entry: PublisherSourceCatalogEntry;
}

function displayTimestamp(value: string): string {
  const timestamp = new Date(value);
  if (Number.isNaN(timestamp.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(timestamp);
}

function displayCoverage(value: number): string {
  return Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1);
}

function PublisherSourceCurrent({ entry }: PublisherSourceCurrentProps) {
  const { current, descriptor } = entry;
  const coverage = current.coverage_percent;
  const boundedCoverage = coverage === undefined ? undefined : Math.max(0, Math.min(100, coverage));
  const period = descriptor.period_kind === "windowed" && current.period_start && current.period_end
    ? { start: current.period_start, end: current.period_end }
    : null;

  return <div className="mqtt-source-current">
    <div>
      <span className={`mqtt-source-quality is-${current.quality}`}>{current.quality}</span>
      {descriptor.period_kind === "windowed" && <span
        className="mqtt-source-coverage"
        role="progressbar"
        aria-label={coverage === undefined ? "Coverage unavailable" : `Coverage ${displayCoverage(coverage)} percent`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={boundedCoverage}
      >{coverage === undefined ? "Coverage unavailable" : `${displayCoverage(coverage)}% coverage`}</span>}
    </div>
    {period
      ? <small aria-label={`Period from ${period.start} to ${period.end}`}>
          <time dateTime={period.start} title={period.start}>{displayTimestamp(period.start)}</time>
          <span aria-hidden="true"> → </span>
          <time dateTime={period.end} title={period.end}>{displayTimestamp(period.end)}</time>
        </small>
      : current.observed_at && <small aria-label={`Observed at ${current.observed_at}`}>
          Observed <time dateTime={current.observed_at} title={current.observed_at}>{displayTimestamp(current.observed_at)}</time>
        </small>}
  </div>;
}

export default PublisherSourceCurrent;
