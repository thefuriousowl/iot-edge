import type { DataLoggerConfig, DataLoggerMode } from "../../../types/datalogger";

export const bytesPerMiB = 1024 * 1024;
export const defaultDataLoggerLimitMiB = 100;
export const defaultEstimatedRowBytes = 384;
export const maxDataLoggerLimitMiB = 1024 * 1024;

interface StorageEstimateInput {
  maxSizeBytes: number | null;
  tagCount: number;
  averageRowBytes?: number;
  batchCount?: number;
  mode: DataLoggerMode;
  config: DataLoggerConfig;
}

export interface DataLoggerStorageEstimate {
  capacityRows: number;
  capacityBatches: number;
  estimatedRetentionSeconds: number;
  estimatedSecondsUntilRollover: number;
}

function secondsPerBatch(mode: DataLoggerMode, config: DataLoggerConfig): number {
  if (mode === "interval" && "interval_seconds" in config) return config.interval_seconds;
  if (!("unit" in config)) return 0;
  if (config.unit === "minute") return config.every * 60;
  if (config.unit === "hour") return config.every * 60 * 60;
  if (config.unit === "day") return config.every * 24 * 60 * 60 / Math.max(1, config.times?.length ?? 0);
  return config.every * 7 * 24 * 60 * 60 / Math.max(1, (config.weekdays?.length ?? 0) * (config.times?.length ?? 0));
}

export function estimateDataLoggerStorage({ maxSizeBytes, tagCount, averageRowBytes = defaultEstimatedRowBytes, batchCount = 0, mode, config }: StorageEstimateInput): DataLoggerStorageEstimate | null {
  if (maxSizeBytes === null || maxSizeBytes < 1 || tagCount < 1 || averageRowBytes < 1) return null;
  const rawCapacity = Math.floor(maxSizeBytes / averageRowBytes);
  const capacityBatches = Math.floor(rawCapacity / tagCount);
  const capacityRows = capacityBatches * tagCount;
  const cadenceSeconds = secondsPerBatch(mode, config);
  return {
    capacityRows,
    capacityBatches,
    estimatedRetentionSeconds: capacityBatches * cadenceSeconds,
    estimatedSecondsUntilRollover: Math.max(0, capacityBatches - batchCount) * cadenceSeconds,
  };
}

export function formatStorageBytes(bytes: number): string {
  if (bytes < 1024) return `${Math.max(0, Math.round(bytes))} B`;
  if (bytes < bytesPerMiB) return `${Math.max(0, Math.round(bytes / 1024))} KiB`;
  if (bytes < 1024 * bytesPerMiB) return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(bytes / bytesPerMiB)} MiB`;
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(bytes / (1024 * bytesPerMiB))} GiB`;
}

export function formatEstimatedDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "Less than one capture";
  const units = [
    { label: "year", seconds: 365 * 24 * 60 * 60 },
    { label: "day", seconds: 24 * 60 * 60 },
    { label: "hour", seconds: 60 * 60 },
    { label: "minute", seconds: 60 },
    { label: "second", seconds: 1 },
  ];
  let remaining = Math.floor(seconds);
  const parts: string[] = [];
  for (const unit of units) {
    const count = Math.floor(remaining / unit.seconds);
    if (count === 0) continue;
    parts.push(`${count} ${unit.label}${count === 1 ? "" : "s"}`);
    remaining -= count * unit.seconds;
    if (parts.length === 2) break;
  }
  return parts.join(" ");
}
