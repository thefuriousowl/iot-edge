import { Activity, Clock3 } from "lucide-react";

import type { MQTTPublisherDraft, PublisherSourceDraft } from "../../../types/publisher";

interface PublisherTriggerSetupProps {
  trigger: MQTTPublisherDraft["trigger"];
  sources: PublisherSourceDraft[];
  onChange: (trigger: MQTTPublisherDraft["trigger"]) => void;
}

function PublisherTriggerSetup({ trigger, sources, onChange }: PublisherTriggerSetupProps) {
  const onChangeAlias = sources.some((source) => source.alias === trigger.source_alias)
    ? trigger.source_alias
    : sources[0]?.alias ?? "";

  return <div className="mqtt-subsection">
    <div className="mqtt-subsection-heading"><Clock3 size={18} /><div><h3>Publisher trigger</h3><p>Controls when Core snapshots already-available values. It never changes Datasource polling or requests a Device.</p></div></div>
    <div className="mqtt-mode-grid">
      <button className={trigger.mode === "interval" ? "is-selected" : ""} type="button" onClick={() => onChange({ ...trigger, mode: "interval" })}><Clock3 /><strong>Fixed interval</strong><small>Snapshot every selected source's latest value on a timer.</small></button>
      <button className={trigger.mode === "on_change" ? "is-selected" : ""} type="button" onClick={() => onChange({ ...trigger, mode: "on_change", source_alias: onChangeAlias })}><Activity /><strong>On source change</strong><small>Coalesce one alias, retaining other sources at their own timestamps.</small></button>
    </div>
    <div className="mqtt-form-grid">
      {trigger.mode === "interval"
        ? <label><span>Trigger interval (ms)</span><input aria-label="Trigger interval" type="number" min="100" max="86400000" value={trigger.interval_ms} onChange={(event) => onChange({ ...trigger, interval_ms: Number(event.target.value) })} /></label>
        : <><label><span>Trigger source</span><select aria-label="Trigger source" value={trigger.source_alias} onChange={(event) => onChange({ ...trigger, source_alias: event.target.value })}>{sources.map((source) => <option key={source.id} value={source.alias}>{source.alias}</option>)}</select></label><label><span>Coalesce window (ms)</span><input aria-label="Coalesce window" type="number" min="1" max="60000" value={trigger.coalesce_ms} onChange={(event) => onChange({ ...trigger, coalesce_ms: Number(event.target.value) })} /></label></>}
    </div>
  </div>;
}

export default PublisherTriggerSetup;
