import type {
  EnergyHistoryParams,
  EnergyHistoryResponse,
  EnergyLiveEvent,
  EnergyMeasurementRun,
  EnergyOverviewResponse,
  EnergyStreamContext,
} from "../types/plugin";
import api, { getAccessToken } from "./api";

function energyPath(id: string): string {
  return `/plugins/${encodeURIComponent(id)}/energy`;
}

export async function getEnergyOverview(id: string, signal?: AbortSignal): Promise<EnergyOverviewResponse> {
  const response = await api.get<EnergyOverviewResponse>(`${energyPath(id)}/overview`, { signal });
  return response.data;
}

export async function getEnergyHistory(id: string, params: EnergyHistoryParams, signal?: AbortSignal): Promise<EnergyHistoryResponse> {
  const response = await api.get<EnergyHistoryResponse>(`${energyPath(id)}/history`, { params, signal });
  return response.data;
}

export async function exportEnergyCSV(id: string, params: EnergyHistoryParams): Promise<Blob> {
  const response = await api.get<Blob>(`${energyPath(id)}/export.csv`, { params, responseType: "blob" });
  return response.data;
}

export async function resetEnergyMeasurement(id: string, input: { expected_run_id: string; name: string; reason: string }): Promise<EnergyMeasurementRun> {
  const response = await api.post<EnergyMeasurementRun>(`${energyPath(id)}/reset`, input);
  return response.data;
}

export async function getEnergyArchives(id: string, signal?: AbortSignal): Promise<EnergyMeasurementRun[]> {
  const response = await api.get<{ data: EnergyMeasurementRun[] }>(`${energyPath(id)}/archives`, { signal });
  return response.data.data;
}

export async function exportEnergyArchiveCSV(id: string, runId: string): Promise<Blob> {
  const response = await api.get<Blob>(`${energyPath(id)}/archives/${encodeURIComponent(runId)}/export.csv`, { responseType: "blob" });
  return response.data;
}

interface PendingEnergyEvent {
  id: string;
  event: string;
  data: string[];
}

function dispatchEnergyEvent(pending: PendingEnergyEvent, context: EnergyStreamContext): void {
  if (pending.event === "energy_reset" && pending.data.length > 0) {
    try {
      const payload = JSON.parse(pending.data.join("\n")) as { reason?: unknown };
      if (typeof payload.reason === "string") context.onReset(payload.reason);
    } catch {
      return;
    }
    return;
  }
  if (pending.event !== "energy_metrics" || pending.data.length === 0) return;
  try {
    const event = JSON.parse(pending.data.join("\n")) as EnergyLiveEvent;
    if (pending.id && event.id !== pending.id) return;
    context.onMessage(event);
  } catch {
    return;
  }
}

async function consumeEnergyStream(response: Response, context: EnergyStreamContext): Promise<void> {
  const reader = response.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let pending: PendingEnergyEvent = { id: "", event: "", data: [] };

  const processLine = (line: string) => {
    if (line === "") {
      dispatchEnergyEvent(pending, context);
      pending = { id: "", event: "", data: [] };
      return;
    }
    if (line.startsWith(":")) return;
    const separator = line.indexOf(":");
    const field = separator === -1 ? line : line.slice(0, separator);
    let value = separator === -1 ? "" : line.slice(separator + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "id") pending.id = value;
    if (field === "event") pending.event = value;
    if (field === "data") pending.data.push(value);
    if (field === "retry" && /^\d+$/.test(value)) context.onRetry(Number(value));
  };

  while (true) {
    const { done, value } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    let newline = buffer.indexOf("\n");
    while (newline !== -1) {
      processLine(buffer.slice(0, newline).replace(/\r$/, ""));
      buffer = buffer.slice(newline + 1);
      newline = buffer.indexOf("\n");
    }
    if (done) {
      if (buffer) processLine(buffer.replace(/\r$/, ""));
      dispatchEnergyEvent(pending, context);
      return;
    }
  }
}

export async function monitorEnergy(id: string, context: EnergyStreamContext): Promise<void> {
  const baseURL = import.meta.env.VITE_API_URL || "/api";
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (context.lastEventId) headers["Last-Event-ID"] = context.lastEventId;
  const response = await fetch(`${baseURL}${energyPath(id)}/stream`, {
    headers,
    credentials: "include",
    signal: context.signal,
  });
  if (!response.ok || !response.body) throw new Error(`Energy live stream failed with status ${response.status}`);
  context.onOpen();
  await consumeEnergyStream(response, context);
}
