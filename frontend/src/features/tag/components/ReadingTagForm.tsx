import axios from "axios";
import {
  ArrowLeft,
  CircleAlert,
  Database,
  Eye,
  LoaderCircle,
  RadioTower,
  Save,
} from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { listDatasources, listDevices } from "../../../services/device.service";
import { createTag, previewTag } from "../../../services/tag.service";
import { listVGateways } from "../../../services/vgateway.service";
import type { Datasource, Device } from "../../../types/device";
import type {
  CreateReadingTagRequest,
  PreviewTagRequest,
  ReadingTagConfigInput,
  TagByteOrder,
  TagDataType,
} from "../../../types/tag";
import type { VGatewayListItem } from "../../../types/vgateway";
import "./ReadingTagForm.css";
import TagPreviewPanel, { type TagPreviewState } from "./TagPreviewPanel";

interface ReadingTagFormProps {
  onCancel: () => void;
  onChangeType: () => void;
  onCreated: () => void;
}

type SourceScope = "gateways" | "devices" | "datasources";

const dataTypes: TagDataType[] = [
  "bool",
  "int16",
  "uint16",
  "int32",
  "uint32",
  "float32",
  "float64",
];

const byteOrders: { value: TagByteOrder; label: string }[] = [
  { value: "big_endian", label: "Big endian (ABCD)" },
  { value: "word_swap", label: "Word swap (CDAB)" },
  { value: "byte_swap", label: "Byte swap (BADC)" },
  { value: "little_endian", label: "Little endian (DCBA)" },
];

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { error?: { message?: string } } | undefined;
    if (data?.error?.message) {
      return data.error.message;
    }
  }
  return fallback;
}

function typeLabel(value: string): string {
  return value
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function optionLabel(name: string, enabled: boolean, type?: string): string {
  return `${name}${type ? ` · ${typeLabel(type)}` : ""}${enabled ? "" : " · Paused"}`;
}

function ReadingTagForm({ onCancel, onChangeType, onCreated }: ReadingTagFormProps) {
  const [gateways, setGateways] = useState<VGatewayListItem[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [datasources, setDatasources] = useState<Datasource[]>([]);
  const [gatewayID, setGatewayID] = useState("");
  const [deviceID, setDeviceID] = useState("");
  const [datasourceID, setDatasourceID] = useState("");
  const [dataType, setDataType] = useState<TagDataType>("uint16");
  const [byteOffset, setByteOffset] = useState("0");
  const [byteOrder, setByteOrder] = useState<TagByteOrder>("big_endian");
  const [bitOffset, setBitOffset] = useState("0");
  const [loadingGateways, setLoadingGateways] = useState(true);
  const [loadingDevices, setLoadingDevices] = useState(false);
  const [loadingDatasources, setLoadingDatasources] = useState(false);
  const [gatewayLoadVersion, setGatewayLoadVersion] = useState(0);
  const [deviceLoadVersion, setDeviceLoadVersion] = useState(0);
  const [datasourceLoadVersion, setDatasourceLoadVersion] = useState(0);
  const [sourceError, setSourceError] = useState<{ scope: SourceScope; message: string } | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [previewState, setPreviewState] = useState<TagPreviewState>({ status: "idle" });

  useEffect(() => {
    const controller = new AbortController();
    void listVGateways({ page: 1, per_page: 100 }, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) {
          setGateways(response.data);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setSourceError({ scope: "gateways", message: errorMessage(error, "Unable to load vGateways.") });
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoadingGateways(false);
        }
      });
    return () => controller.abort();
  }, [gatewayLoadVersion]);

  useEffect(() => {
    if (!gatewayID) {
      return;
    }

    const controller = new AbortController();
    void listDevices(gatewayID, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) {
          setDevices(response);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setSourceError({ scope: "devices", message: errorMessage(error, "Unable to load devices.") });
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoadingDevices(false);
        }
      });
    return () => controller.abort();
  }, [deviceLoadVersion, gatewayID]);

  useEffect(() => {
    if (!deviceID) {
      return;
    }

    const controller = new AbortController();
    void listDatasources(deviceID, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) {
          setDatasources(response);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setSourceError({ scope: "datasources", message: errorMessage(error, "Unable to load datasources.") });
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoadingDatasources(false);
        }
      });
    return () => controller.abort();
  }, [datasourceLoadVersion, deviceID]);

  function changeGateway(nextGatewayID: string) {
    setPreviewState({ status: "idle" });
    setSourceError(null);
    setGatewayID(nextGatewayID);
    setDevices([]);
    setDeviceID("");
    setDatasources([]);
    setDatasourceID("");
    setLoadingDevices(Boolean(nextGatewayID));
    setLoadingDatasources(false);
  }

  function changeDevice(nextDeviceID: string) {
    setPreviewState({ status: "idle" });
    setSourceError(null);
    setDeviceID(nextDeviceID);
    setDatasources([]);
    setDatasourceID("");
    setLoadingDatasources(Boolean(nextDeviceID));
  }

  function retrySource() {
    if (sourceError?.scope === "gateways") {
      setSourceError(null);
      setLoadingGateways(true);
      setGatewayLoadVersion((version) => version + 1);
    } else if (sourceError?.scope === "devices") {
      setSourceError(null);
      setLoadingDevices(true);
      setDeviceLoadVersion((version) => version + 1);
    } else if (sourceError?.scope === "datasources") {
      setSourceError(null);
      setLoadingDatasources(true);
      setDatasourceLoadVersion((version) => version + 1);
    }
  }

  function changeDatasource(nextDatasourceID: string) {
    setDatasourceID(nextDatasourceID);
    setPreviewState({ status: "idle" });
  }

  function changeDataType(nextDataType: TagDataType) {
    setDataType(nextDataType);
    setBitOffset("0");
    setPreviewState({ status: "idle" });
  }

  function readingConfig(): ReadingTagConfigInput {
    return {
      decoder: {
        type: "binary_numeric",
        config: {
          byte_offset: Number(byteOffset),
          byte_order: byteOrder,
          ...(dataType === "bool" ? { bit_offset: Number(bitOffset) } : {}),
        },
      },
    };
  }

  async function preview(form: HTMLFormElement) {
    const byteOffsetInput = form.elements.namedItem("byte_offset") as HTMLInputElement | null;
    const bitOffsetInput = form.elements.namedItem("bit_offset") as HTMLInputElement | null;
    if (!byteOffsetInput?.reportValidity() || (bitOffsetInput && !bitOffsetInput.reportValidity())) {
      return;
    }

    const request: PreviewTagRequest = {
      type: "reading",
      datasource_id: datasourceID,
      data_type: dataType,
      config: readingConfig(),
    };
    setPreviewState({ status: "loading" });
    try {
      setPreviewState({ status: "success", result: await previewTag(request) });
    } catch (error) {
      setPreviewState({ status: "error", message: errorMessage(error, "Unable to preview this Reading Tag.") });
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    if (!form.reportValidity()) {
      return;
    }

    const data = new FormData(form);
    const request: CreateReadingTagRequest = {
      name: String(data.get("name")).trim(),
      type: "reading",
      data_type: dataType,
      datasource_id: datasourceID,
      description: String(data.get("description") || "").trim() || null,
      enabled: data.get("enabled") === "on",
      config: readingConfig(),
    };

    setSubmitting(true);
    setSubmitError(null);
    try {
      await createTag(request);
      onCreated();
    } catch (error) {
      setSubmitError(errorMessage(error, "Unable to create this Reading Tag."));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <section className="reading-tag-step" aria-labelledby="reading-tag-heading">
      <header className="reading-tag-heading">
        <div>
          <span>Reading Tag</span>
          <h1 id="reading-tag-heading">Configure datasource decoding</h1>
          <p>Bind a datasource and decode its raw payload into a typed value.</p>
        </div>
        <button type="button" onClick={onChangeType}>
          <ArrowLeft aria-hidden="true" size={17} />
          Change type
        </button>
      </header>

      {submitError && (
        <div className="reading-tag-alert" role="alert">
          <CircleAlert aria-hidden="true" size={18} />
          <span>{submitError}</span>
          <button type="button" aria-label="Dismiss error" onClick={() => setSubmitError(null)}>×</button>
        </div>
      )}

      <form className="reading-tag-form" onSubmit={(event) => void submit(event)}>
        <section className="reading-tag-section">
          <div className="reading-tag-section-title">
            <RadioTower aria-hidden="true" size={19} />
            <div><h2>Tag details</h2><p>Name and describe the normalized value.</p></div>
          </div>
          <div className="reading-tag-grid is-two-column">
            <label className="reading-tag-field">
              <span>Name <b>*</b></span>
              <input name="name" required maxLength={100} placeholder="line_voltage_l1" />
            </label>
            <label className="reading-tag-field">
              <span>Data type <b>*</b></span>
              <select name="data_type" value={dataType} onChange={(event) => changeDataType(event.target.value as TagDataType)}>
                {dataTypes.map((value) => <option key={value} value={value}>{value}</option>)}
              </select>
            </label>
            <label className="reading-tag-field is-wide">
              <span>Description</span>
              <textarea name="description" maxLength={1000} placeholder="Optional context for operators" />
            </label>
            <label className="reading-tag-toggle is-wide">
              <input name="enabled" type="checkbox" defaultChecked />
              <span><i /><strong>Enabled</strong><small>Allow this tag to be previewed and evaluated.</small></span>
            </label>
          </div>
        </section>

        <section className="reading-tag-section">
          <div className="reading-tag-section-title">
            <Database aria-hidden="true" size={19} />
            <div><h2>Datasource</h2><p>Locate the source through the protocol-neutral acquisition hierarchy.</p></div>
          </div>
          {sourceError && (
            <div className="reading-source-error" role="alert">
              <CircleAlert aria-hidden="true" size={17} />
              <span>{sourceError.message}</span>
              <button type="button" onClick={retrySource}>Retry</button>
            </div>
          )}
          <div className="reading-tag-grid is-three-column">
            <label className="reading-tag-field">
              <span>vGateway <b>*</b></span>
              <select aria-label="vGateway" required value={gatewayID} disabled={loadingGateways} onChange={(event) => changeGateway(event.target.value)}>
                <option value="">{loadingGateways ? "Loading vGateways…" : "Select vGateway"}</option>
                {gateways.map((gateway) => <option key={gateway.id} value={gateway.id}>{optionLabel(gateway.name, gateway.enabled, gateway.type)}</option>)}
              </select>
            </label>
            <label className="reading-tag-field">
              <span>Device <b>*</b></span>
              <select aria-label="Device" required value={deviceID} disabled={!gatewayID || loadingDevices} onChange={(event) => changeDevice(event.target.value)}>
                <option value="">{loadingDevices ? "Loading devices…" : "Select device"}</option>
                {devices.map((device) => <option key={device.id} value={device.id}>{optionLabel(device.name, device.enabled, device.type)}</option>)}
              </select>
            </label>
            <label className="reading-tag-field">
              <span>Datasource <b>*</b></span>
              <select aria-label="Datasource" required value={datasourceID} disabled={!deviceID || loadingDatasources} onChange={(event) => changeDatasource(event.target.value)}>
                <option value="">{loadingDatasources ? "Loading datasources…" : "Select datasource"}</option>
                {datasources.map((datasource) => <option key={datasource.id} value={datasource.id}>{optionLabel(datasource.name, datasource.enabled, datasource.type)}</option>)}
              </select>
            </label>
          </div>
          {!loadingGateways && gateways.length === 0 && !sourceError && (
            <p className="reading-source-empty">No vGateways are configured. <Link to="/vgateways/new">Add a vGateway first.</Link></p>
          )}
        </section>

        <section className="reading-tag-section">
          <div className="reading-tag-section-title">
            <RadioTower aria-hidden="true" size={19} />
            <div><h2>Decoder</h2><p>Interpret raw datasource bytes without coupling the Tag to a transport protocol.</p></div>
          </div>
          <div className="reading-tag-grid is-four-column">
            <label className="reading-tag-field">
              <span>Decoder <b>*</b></span>
              <select name="decoder_type" defaultValue="binary_numeric" disabled>
                <option value="binary_numeric">Binary numeric</option>
              </select>
            </label>
            <label className="reading-tag-field">
              <span>Byte offset <b>*</b></span>
              <input name="byte_offset" type="number" min="0" value={byteOffset} required onChange={(event) => { setByteOffset(event.target.value); setPreviewState({ status: "idle" }); }} />
            </label>
            <label className="reading-tag-field">
              <span>Byte order <b>*</b></span>
              <select name="byte_order" value={byteOrder} onChange={(event) => { setByteOrder(event.target.value as TagByteOrder); setPreviewState({ status: "idle" }); }}>
                {byteOrders.map((order) => <option key={order.value} value={order.value}>{order.label}</option>)}
              </select>
            </label>
            {dataType === "bool" && (
              <label className="reading-tag-field">
                <span>Bit offset <b>*</b></span>
                <input name="bit_offset" type="number" min="0" max="7" value={bitOffset} required onChange={(event) => { setBitOffset(event.target.value); setPreviewState({ status: "idle" }); }} />
              </label>
            )}
          </div>
          <p className="reading-tag-help">
            Byte offset is zero-based in the datasource raw payload, not the protocol address. A 16-bit register uses 2 bytes, so register offsets 0, 2, 4, and 6 map to byte offsets 0, 4, 8, and 12.
          </p>
        </section>

        <TagPreviewPanel state={previewState} />

        <footer className="reading-tag-actions">
          <button type="button" onClick={onCancel}>Cancel</button>
          <button type="button" disabled={submitting || previewState.status === "loading" || !datasourceID} onClick={(event) => void preview(event.currentTarget.form!)}>
            {previewState.status === "loading" ? <LoaderCircle aria-hidden="true" className="is-spinning" size={17} /> : <Eye aria-hidden="true" size={17} />}
            {previewState.status === "loading" ? "Previewing…" : "Preview value"}
          </button>
          <button className="is-primary" type="submit" disabled={submitting || !datasourceID}>
            {submitting ? <LoaderCircle aria-hidden="true" className="is-spinning" size={17} /> : <Save aria-hidden="true" size={17} />}
            {submitting ? "Creating…" : "Create Reading Tag"}
          </button>
        </footer>
      </form>
    </section>
  );
}

export default ReadingTagForm;
