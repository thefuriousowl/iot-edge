import axios from "axios";
import { CheckCircle2, CircleAlert, Eye, LoaderCircle, Plus, Save } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";

import { createTag, listTags, previewTag, validateTagExpression } from "../../../services/tag.service";
import type { CreateCalculatedTagRequest, Tag, TagDataType } from "../../../types/tag";
import "./CalculatedTagForm.css";
import TagPreviewPanel, { type TagPreviewState } from "./TagPreviewPanel";

interface CalculatedTagFormProps {
  onCancel: () => void;
  onChangeType: () => void;
  onCreated: () => void;
}

type ValidationState =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "checking" }
  | { status: "valid"; dependencies: string[] }
  | { status: "invalid"; message: string };

const dataTypes: TagDataType[] = ["bool", "int16", "uint16", "int32", "uint32", "float32", "float64"];

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { error?: { message?: string } } | undefined;
    if (data?.error?.message) return data.error.message;
  }
  return fallback;
}

function CalculatedTagForm({ onCancel, onChangeType, onCreated }: CalculatedTagFormProps) {
  const [tags, setTags] = useState<Tag[]>([]);
  const [loadingTags, setLoadingTags] = useState(true);
  const [tagLoadError, setTagLoadError] = useState<string | null>(null);
  const [tagLoadVersion, setTagLoadVersion] = useState(0);
  const [dataType, setDataType] = useState<TagDataType>("float64");
  const [expression, setExpression] = useState("");
  const [validation, setValidation] = useState<ValidationState>({ status: "idle" });
  const [validationVersion, setValidationVersion] = useState(0);
  const [previewState, setPreviewState] = useState<TagPreviewState>({ status: "idle" });
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const expressionRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    void listTags({ page: 1, per_page: 100 }, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) setTags(response.data);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setTagLoadError(errorMessage(error, "Unable to load Tags for expression references."));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingTags(false);
      });
    return () => controller.abort();
  }, [tagLoadVersion]);

  useEffect(() => {
    const trimmed = expression.trim();
    if (!trimmed) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setValidation({ status: "checking" });
      void validateTagExpression({ expression: trimmed }, controller.signal)
        .then((response) => {
          if (!controller.signal.aborted) setValidation({ status: "valid", dependencies: response.dependencies });
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted) setValidation({ status: "invalid", message: errorMessage(error, "Expression validation failed.") });
        });
    }, 350);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [expression, validationVersion]);

  function changeExpression(nextExpression: string) {
    setExpression(nextExpression);
    setValidation({ status: nextExpression.trim() ? "pending" : "idle" });
    setPreviewState({ status: "idle" });
  }

  function insertTag(tag: Tag) {
    const textarea = expressionRef.current;
    const token = `\${${tag.id}}`;
    const start = textarea?.selectionStart ?? expression.length;
    const end = textarea?.selectionEnd ?? expression.length;
    const nextExpression = `${expression.slice(0, start)}${token}${expression.slice(end)}`;
    changeExpression(nextExpression);
    window.setTimeout(() => {
      expressionRef.current?.focus();
      expressionRef.current?.setSelectionRange(start + token.length, start + token.length);
    }, 0);
  }

  async function preview() {
    if (validation.status !== "valid") return;
    setPreviewState({ status: "loading" });
    try {
      setPreviewState({
        status: "success",
        result: await previewTag({ type: "calculated", data_type: dataType, config: { expression: expression.trim() } }),
      });
    } catch (error) {
      setPreviewState({ status: "error", message: errorMessage(error, "Unable to preview this Calculated Tag.") });
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!event.currentTarget.reportValidity() || validation.status !== "valid") return;
    const form = new FormData(event.currentTarget);
    const request: CreateCalculatedTagRequest = {
      name: String(form.get("name")).trim(),
      type: "calculated",
      data_type: dataType,
      description: String(form.get("description")).trim() || null,
      enabled: form.get("enabled") === "on",
      config: { expression: expression.trim() },
    };
    setSubmitting(true);
    setSubmitError(null);
    try {
      await createTag(request);
      onCreated();
    } catch (error) {
      setSubmitError(errorMessage(error, "Unable to create this Calculated Tag."));
    } finally {
      setSubmitting(false);
    }
  }

  const tagNames = new Map(tags.map((tag) => [tag.id, tag.name]));
  const ready = validation.status === "valid";

  return (
    <form className="calculated-tag-form" aria-label="Configure a calculated expression" onSubmit={(event) => void submit(event)}>
      <header className="calculated-tag-header">
        <div><span>Calculated Tag</span><h1>Configure an expression</h1><p>Combine typed Tag values with a validated, dependency-aware expression.</p></div>
        <button type="button" onClick={onChangeType}>Change type</button>
      </header>

      {submitError && <div className="calculated-tag-alert" role="alert"><CircleAlert size={18} /><span>{submitError}</span><button type="button" onClick={() => setSubmitError(null)}>Dismiss</button></div>}

      <section className="calculated-tag-section">
        <div><h2>Tag details</h2><p>Name and type the derived value.</p></div>
        <div className="calculated-tag-grid">
          <label><span>Name <b>*</b></span><input name="name" required maxLength={100} placeholder="voltage_delta" /></label>
          <label><span>Data type <b>*</b></span><select name="data_type" value={dataType} onChange={(event) => { setDataType(event.target.value as TagDataType); setPreviewState({ status: "idle" }); }}>{dataTypes.map((type) => <option key={type} value={type}>{type}</option>)}</select></label>
          <label className="is-wide"><span>Description</span><textarea name="description" maxLength={500} placeholder="Optional context for operators" /></label>
          <label className="calculated-tag-toggle"><input name="enabled" type="checkbox" defaultChecked /><span><i /><strong>Enabled</strong><small>Allow this calculation to be previewed and referenced.</small></span></label>
        </div>
      </section>

      <section className="calculated-tag-section calculated-expression-section">
        <div><h2>Expression</h2><p>Use arithmetic, comparison, boolean operators, parentheses, and Tag references.</p></div>
        <div className="calculated-expression-layout">
          <div className="calculated-editor">
            <label><span>Expression <b>*</b></span><textarea ref={expressionRef} name="expression" required maxLength={2048} value={expression} onChange={(event) => changeExpression(event.target.value)} placeholder="Select Tags to insert references, then add operators" spellCheck={false} /></label>
            <code>Operators: + − * / % · == != &lt; &lt;= &gt; &gt;= · &amp;&amp; || !</code>
            <ValidationResult state={validation} tagNames={tagNames} onRetry={() => { setValidation({ status: "pending" }); setValidationVersion((version) => version + 1); }} />
          </div>
          <aside className="calculated-tag-palette" aria-label="Available Tags">
            <div><strong>Insert Tag</strong><small>References use immutable IDs.</small></div>
            {loadingTags ? <p><LoaderCircle className="is-spinning" size={16} /> Loading Tags…</p> : tagLoadError ? <div role="alert"><span>{tagLoadError}</span><button type="button" onClick={() => { setTagLoadError(null); setLoadingTags(true); setTagLoadVersion((version) => version + 1); }}>Retry</button></div> : tags.length === 0 ? <p>No Tags are available yet. Literal-only expressions still work.</p> : <ul>{tags.map((tag) => <li key={tag.id}><button type="button" aria-label={`Insert ${tag.name}`} onClick={() => insertTag(tag)}><Plus size={14} /><span><strong>{tag.name}</strong><small>{tag.type} · {tag.data_type}{tag.enabled ? "" : " · disabled"}</small></span></button></li>)}</ul>}
          </aside>
        </div>
      </section>

      <TagPreviewPanel state={previewState} />

      <footer className="calculated-tag-actions">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button type="button" disabled={!ready || submitting || previewState.status === "loading"} onClick={() => void preview()}>{previewState.status === "loading" ? <LoaderCircle className="is-spinning" size={17} /> : <Eye size={17} />}{previewState.status === "loading" ? "Previewing…" : "Preview value"}</button>
        <button className="is-primary" type="submit" disabled={!ready || submitting}>{submitting ? <LoaderCircle className="is-spinning" size={17} /> : <Save size={17} />}{submitting ? "Creating…" : "Create Calculated Tag"}</button>
      </footer>
    </form>
  );
}

function ValidationResult({ state, tagNames, onRetry }: { state: ValidationState; tagNames: Map<string, string>; onRetry: () => void }) {
  if (state.status === "idle") return <p className="calculated-validation is-idle">Enter an expression to validate it.</p>;
  if (state.status === "pending" || state.status === "checking") return <p className="calculated-validation is-checking"><LoaderCircle className="is-spinning" size={16} /> {state.status === "pending" ? "Waiting for changes…" : "Validating expression…"}</p>;
  if (state.status === "invalid") return <div className="calculated-validation is-invalid" role="alert"><CircleAlert size={16} /><span>{state.message}</span><button type="button" onClick={onRetry}>Retry</button></div>;
  return <div className="calculated-validation is-valid" role="status"><CheckCircle2 size={16} /><span>Valid expression · {state.dependencies.length} {state.dependencies.length === 1 ? "dependency" : "dependencies"}</span>{state.dependencies.length > 0 && <ul>{state.dependencies.map((id) => <li key={id}>{tagNames.get(id) ?? `${id.slice(0, 8)}…`}</li>)}</ul>}</div>;
}

export default CalculatedTagForm;
