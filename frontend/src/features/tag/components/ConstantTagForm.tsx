import axios from "axios";
import {
  ArrowLeft,
  CircleAlert,
  Eye,
  Hash,
  LoaderCircle,
  Save,
} from "lucide-react";
import { type FormEvent, useState } from "react";

import { createTag, previewTag } from "../../../services/tag.service";
import type {
  CreateConstantTagRequest,
  PreviewTagRequest,
  TagDataType,
  TagValue,
} from "../../../types/tag";
import "./ConstantTagForm.css";
import TagPreviewPanel, { type TagPreviewState } from "./TagPreviewPanel";

interface ConstantTagFormProps {
  onCancel: () => void;
  onChangeType: () => void;
  onCreated: () => void;
}

interface NumericConstraint {
  min?: string;
  max?: string;
  step: string;
}

const dataTypes: TagDataType[] = [
  "bool",
  "int16",
  "uint16",
  "int32",
  "uint32",
  "float32",
  "float64",
];

const numericConstraints: Record<Exclude<TagDataType, "bool">, NumericConstraint> = {
  int16: { min: "-32768", max: "32767", step: "1" },
  uint16: { min: "0", max: "65535", step: "1" },
  int32: { min: "-2147483648", max: "2147483647", step: "1" },
  uint32: { min: "0", max: "4294967295", step: "1" },
  float32: { min: "-3.4028235e38", max: "3.4028235e38", step: "any" },
  float64: { step: "any" },
};

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { error?: { message?: string } } | undefined;
    if (data?.error?.message) {
      return data.error.message;
    }
  }
  return fallback;
}

function ConstantTagForm({ onCancel, onChangeType, onCreated }: ConstantTagFormProps) {
  const [dataType, setDataType] = useState<TagDataType>("float64");
  const [value, setValue] = useState("0");
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [previewState, setPreviewState] = useState<TagPreviewState>({ status: "idle" });

  function changeDataType(nextDataType: TagDataType) {
    setDataType(nextDataType);
    setValue(nextDataType === "bool" ? "false" : "0");
    setPreviewState({ status: "idle" });
  }

  function changeValue(nextValue: string) {
    setValue(nextValue);
    setPreviewState({ status: "idle" });
  }

  function typedValue(): TagValue {
    return dataType === "bool" ? value === "true" : Number(value);
  }

  function valueIsValid(form: HTMLFormElement): boolean {
    const input = form.elements.namedItem("value") as HTMLInputElement | HTMLSelectElement | null;
    return input?.reportValidity() ?? false;
  }

  async function preview(form: HTMLFormElement) {
    if (!valueIsValid(form)) {
      return;
    }
    const request: PreviewTagRequest = {
      type: "constant",
      data_type: dataType,
      config: { value: typedValue() },
    };
    setPreviewState({ status: "loading" });
    try {
      setPreviewState({ status: "success", result: await previewTag(request) });
    } catch (error) {
      setPreviewState({ status: "error", message: errorMessage(error, "Unable to preview this Constant Tag.") });
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    if (!form.reportValidity()) {
      return;
    }
    const data = new FormData(form);
    const request: CreateConstantTagRequest = {
      name: String(data.get("name")).trim(),
      type: "constant",
      data_type: dataType,
      description: String(data.get("description") || "").trim() || null,
      enabled: data.get("enabled") === "on",
      config: { value: typedValue() },
    };
    setSubmitting(true);
    setSubmitError(null);
    try {
      await createTag(request);
      onCreated();
    } catch (error) {
      setSubmitError(errorMessage(error, "Unable to create this Constant Tag."));
    } finally {
      setSubmitting(false);
    }
  }

  const constraint = dataType === "bool" ? null : numericConstraints[dataType];

  return (
    <section className="constant-tag-step" aria-labelledby="constant-tag-heading">
      <header className="constant-tag-heading">
        <div>
          <span>Constant Tag</span>
          <h1 id="constant-tag-heading">Configure a reference value</h1>
          <p>Store a typed value for thresholds, rates, defaults, and calculations.</p>
        </div>
        <button type="button" onClick={onChangeType}>
          <ArrowLeft aria-hidden="true" size={17} />
          Change type
        </button>
      </header>

      {submitError && (
        <div className="constant-tag-alert" role="alert">
          <CircleAlert aria-hidden="true" size={18} />
          <span>{submitError}</span>
          <button type="button" aria-label="Dismiss error" onClick={() => setSubmitError(null)}>×</button>
        </div>
      )}

      <form className="constant-tag-form" onSubmit={(event) => void submit(event)}>
        <section className="constant-tag-section">
          <div className="constant-tag-section-title">
            <Hash aria-hidden="true" size={19} />
            <div><h2>Tag details</h2><p>Name and describe the reusable reference value.</p></div>
          </div>
          <div className="constant-tag-grid is-two-column">
            <label className="constant-tag-field">
              <span>Name <b>*</b></span>
              <input name="name" required maxLength={100} placeholder="nominal_voltage" />
            </label>
            <label className="constant-tag-field">
              <span>Data type <b>*</b></span>
              <select name="data_type" value={dataType} onChange={(event) => changeDataType(event.target.value as TagDataType)}>
                {dataTypes.map((type) => <option key={type} value={type}>{type}</option>)}
              </select>
            </label>
            <label className="constant-tag-field is-wide">
              <span>Description</span>
              <textarea name="description" maxLength={1000} placeholder="Optional context for operators" />
            </label>
            <label className="constant-tag-toggle is-wide">
              <input name="enabled" type="checkbox" defaultChecked />
              <span><i /><strong>Enabled</strong><small>Allow this constant to be previewed and used by calculated Tags.</small></span>
            </label>
          </div>
        </section>

        <section className="constant-tag-section">
          <div className="constant-tag-section-title">
            <Hash aria-hidden="true" size={19} />
            <div><h2>Constant value</h2><p>The input contract follows the selected backend data type.</p></div>
          </div>
          <div className="constant-tag-grid is-value-grid">
            <label className="constant-tag-field">
              <span>Value <b>*</b></span>
              {dataType === "bool" ? (
                <select name="value" value={value} onChange={(event) => changeValue(event.target.value)}>
                  <option value="false">false</option>
                  <option value="true">true</option>
                </select>
              ) : (
                <input
                  name="value"
                  type="number"
                  required
                  value={value}
                  min={constraint?.min}
                  max={constraint?.max}
                  step={constraint?.step}
                  onChange={(event) => changeValue(event.target.value)}
                />
              )}
            </label>
            <div className="constant-tag-range">
              <strong>{dataType}</strong>
              <span>{constraint?.min && constraint.max ? `${constraint.min} to ${constraint.max}` : dataType === "bool" ? "Boolean true or false" : "Finite decimal value"}</span>
            </div>
          </div>
        </section>

        <TagPreviewPanel state={previewState} />

        <footer className="constant-tag-actions">
          <button type="button" onClick={onCancel}>Cancel</button>
          <button type="button" disabled={submitting || previewState.status === "loading"} onClick={(event) => void preview(event.currentTarget.form!)}>
            {previewState.status === "loading" ? <LoaderCircle aria-hidden="true" className="is-spinning" size={17} /> : <Eye aria-hidden="true" size={17} />}
            {previewState.status === "loading" ? "Previewing…" : "Preview value"}
          </button>
          <button className="is-primary" type="submit" disabled={submitting}>
            {submitting ? <LoaderCircle aria-hidden="true" className="is-spinning" size={17} /> : <Save aria-hidden="true" size={17} />}
            {submitting ? "Creating…" : "Create Constant Tag"}
          </button>
        </footer>
      </form>
    </section>
  );
}

export default ConstantTagForm;
