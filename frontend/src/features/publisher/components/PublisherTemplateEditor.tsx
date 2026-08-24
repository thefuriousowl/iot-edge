import { CircleAlert, ShieldCheck } from "lucide-react";
import { useState, type RefObject } from "react";

import type { MQTTPayloadFixture, MQTTPayloadPreview, PublisherPayloadValidation, PublisherSourceDraft } from "../../../types/publisher";
import { mqttPayloadHelpers, mqttPayloadSourceHelpers, mqttPayloadSourceSyntax } from "../utils/mqtt";
import type { MQTTPayloadSourceHelper } from "../utils/mqtt";

const fixtures: Array<{ id: MQTTPayloadFixture; label: string }> = [
  { id: "good", label: "Good" },
  { id: "unavailable", label: "Unavailable" },
  { id: "windowed", label: "Windowed" },
];

interface PublisherTemplateEditorProps {
  editorRef: RefObject<HTMLTextAreaElement | null>;
  editorID: string;
  editorLabel: string;
  template: string;
  sources: PublisherSourceDraft[];
  fixture: MQTTPayloadFixture;
  preview: { result: MQTTPayloadPreview | null; error: string };
  validation: PublisherPayloadValidation | null;
  onTemplateChange: (template: string) => void;
  onFixtureChange: (fixture: MQTTPayloadFixture) => void;
  onInsert: (syntax: string) => void;
  onValidate: () => void;
}

function PublisherTemplateEditor({ editorRef, editorID, editorLabel, template, sources, fixture, preview, validation, onTemplateChange, onFixtureChange, onInsert, onValidate }: PublisherTemplateEditorProps) {
  const [helper, setHelper] = useState<MQTTPayloadSourceHelper>("value");

  return <>
    <div className="mqtt-mapper mqtt-template-palette">
      <header><div><h3>Template syntax palette</h3><p>Choose a helper, then insert syntax for a selected Tag or Plugin output at the editor cursor.</p></div></header>
      <div className="mqtt-palette-controls">
        <label><span>Source helper</span><select aria-label="Payload source helper" value={helper} onChange={(event) => setHelper(event.target.value as MQTTPayloadSourceHelper)}>{mqttPayloadSourceHelpers.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}</select></label>
        <div><span>Selected Tags and Plugin outputs</span><div className="mqtt-source-syntax-list">{sources.map((source) => {
          const syntax = mqttPayloadSourceSyntax(helper, source.alias);
          return <button key={source.id} type="button" aria-label={`Insert ${helper} syntax for ${source.alias}`} onClick={() => onInsert(syntax)}><strong>{source.alias}</strong><code>{syntax}</code></button>;
        })}</div></div>
        <div><span>Publish context</span><div className="mqtt-context-syntax-list">{["{{published_unix_ms}}", "{{published_at}}", "{{publisher_id}}"].map((syntax) => <button key={syntax} type="button" aria-label={`Insert ${syntax}`} onClick={() => onInsert(syntax)}><code>{syntax}</code></button>)}</div></div>
      </div>
    </div>
    <div className="mqtt-editor-grid">
      <div className="mqtt-editor"><label htmlFor={editorID}><span>{editorLabel}</span><small>{new TextEncoder().encode(template).length.toLocaleString()} / 16,384 bytes</small></label><textarea ref={editorRef} id={editorID} aria-label={editorLabel} spellCheck={false} value={template} onChange={(event) => onTemplateChange(event.target.value)} /><details><summary>Allowed helpers ({mqttPayloadHelpers.length})</summary><code>{mqttPayloadHelpers.join(" · ")}</code><p>Examples: {`{{value "active_power_kw"}}`} · {`{{round "active_power_kw" 2}}`} · {`{{default "active_power_kw" 0}}`} · {`{{published_unix_ms}}`}</p></details></div>
      <div className="mqtt-preview"><header><div><strong>Fixture preview</strong>{preview.result && <small>{preview.result.helperCalls} helpers · {preview.result.referencedAliases.length} aliases</small>}</div><div role="tablist" aria-label="Payload fixture">{fixtures.map((option) => <button key={option.id} className={fixture === option.id ? "is-active" : ""} type="button" role="tab" aria-selected={fixture === option.id} onClick={() => onFixtureChange(option.id)}>{option.label}</button>)}</div></header>{preview.error ? <div className="mqtt-preview-error" role="alert"><CircleAlert />{preview.error}</div> : <pre>{preview.result?.rendered}</pre>}</div>
    </div>
    <div className="mqtt-core-validation"><button type="button" onClick={onValidate}><ShieldCheck size={17} />Validate with Core</button><span>{validation ? `Passed · ${validation.helper_calls} helpers · ${validation.referenced_aliases.length} aliases` : "Local fixtures are advisory; Core validation is authoritative."}</span></div>
  </>;
}

export default PublisherTemplateEditor;
