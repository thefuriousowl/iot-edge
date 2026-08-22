import { CheckCircle2, CircleAlert, Eye, LoaderCircle } from "lucide-react";

import type { TagPreviewResult } from "../../../types/tag";
import "./TagPreviewPanel.css";

export type TagPreviewState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "success"; result: TagPreviewResult }
  | { status: "error"; message: string };

interface TagPreviewPanelProps {
  state: TagPreviewState;
}

const observedAtFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
});

function formatValue(value: boolean | number): string {
  return typeof value === "boolean"
    ? String(value)
    : new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value);
}

function formatObservedAt(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown time" : observedAtFormatter.format(date);
}

function TagPreviewPanel({ state }: TagPreviewPanelProps) {
  if (state.status === "idle") {
    return (
      <section className="tag-preview-panel is-idle" aria-label="Tag preview">
        <Eye aria-hidden="true" size={20} />
        <div><strong>Preview value</strong><span>Read and decode the current source without saving the Tag.</span></div>
      </section>
    );
  }

  if (state.status === "loading") {
    return (
      <section className="tag-preview-panel is-loading" role="status">
        <LoaderCircle aria-hidden="true" className="is-spinning" size={20} />
        <div><strong>Reading current value…</strong><span>Executing the datasource and decoder pipeline.</span></div>
      </section>
    );
  }

  if (state.status === "error") {
    return (
      <section className="tag-preview-panel is-error" role="alert">
        <CircleAlert aria-hidden="true" size={20} />
        <div><strong>Preview failed</strong><span>{state.message}</span></div>
      </section>
    );
  }

  const { result } = state;
  return (
    <output className={`tag-preview-panel is-success is-${result.quality}`} aria-label="Tag preview result">
      <CheckCircle2 aria-hidden="true" size={21} />
      <div className="tag-preview-copy">
        <strong>Decoded value</strong>
        <span>{formatObservedAt(result.observed_at)}</span>
      </div>
      <div className="tag-preview-value">
        <strong>{formatValue(result.value)}</strong>
        <span>{result.data_type} · {result.quality}</span>
      </div>
    </output>
  );
}

export default TagPreviewPanel;
