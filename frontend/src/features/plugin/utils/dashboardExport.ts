import type { EnergyHistoryResponse } from "../../../types/plugin";
import { zipSync } from "fflate";
import { toPng } from "html-to-image";

export interface DashboardExportFilters {
  pluginName: string;
  assetID: string;
  assetName: string;
  displayTimezone: string;
  comparePrevious: boolean;
}

function csvCell(value: unknown): string {
  const text = value === null || value === undefined ? "" : String(value);
  return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

function issueText(row: EnergyHistoryResponse["data"][number]): string {
  return [...(row.electrical.issues ?? []), ...(row.thermal.issues ?? [])]
    .map((issue) => `${issue.code}${issue.errors?.length ? `: ${issue.errors.map((error) => error.message).join("; ")}` : ""}`)
    .join(" | ");
}

export function buildEnergyDashboardCSV(
  history: EnergyHistoryResponse,
  previous: EnergyHistoryResponse | null,
  filters: DashboardExportFilters,
): string {
  const previousRows = [...(previous?.data ?? [])].reverse();
  const rows = [...history.data].reverse();
  const header = [
    "plugin_instance_id", "plugin_name", "logger_id", "hierarchy_asset_id", "hierarchy_asset_name",
    "source_timezone", "display_timezone", "bucket", "compare_previous", "from", "to",
    "electrical_kwh", "thermal_kwh", "cost", "currency", "cop",
    "electrical_coverage_percent", "thermal_coverage_percent", "electrical_skipped_seconds",
    "thermal_skipped_seconds", "issues", "previous_from", "previous_to", "previous_electrical_kwh",
    "previous_thermal_kwh", "previous_electrical_coverage_percent", "previous_thermal_coverage_percent",
  ];
  const body = rows.map((row, index) => {
    const prior = filters.comparePrevious ? previousRows[index] : undefined;
    return [
      history.instance_id, filters.pluginName, history.logger_id, filters.assetID, filters.assetName,
      history.timezone, filters.displayTimezone, history.bucket, filters.comparePrevious, row.from, row.to,
      row.electrical.kilowatt_hours, row.thermal.kilowatt_hours, row.cost.valid ? row.cost.value : null,
      history.currency, row.cop.valid ? row.cop.value : null, row.electrical.coverage_percent,
      row.thermal.coverage_percent, row.electrical.skipped_seconds, row.thermal.skipped_seconds, issueText(row),
      prior?.from, prior?.to, prior?.electrical.kilowatt_hours, prior?.thermal.kilowatt_hours,
      prior?.electrical.coverage_percent, prior?.thermal.coverage_percent,
    ].map(csvCell).join(",");
  });
  return `\uFEFF${[header.join(","), ...body].join("\r\n")}\r\n`;
}

export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function pngDataURLBlob(dataURL: string): Blob {
  const [header, encoded] = dataURL.split(",", 2);
  if (!header?.startsWith("data:image/png;base64") || !encoded) throw new Error("PNG renderer returned invalid data");
  const binary = atob(encoded);
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  return new Blob([bytes], { type: "image/png" });
}

function pngDataURLBytes(dataURL: string): Uint8Array {
  const [header, encoded] = dataURL.split(",", 2);
  if (!header?.startsWith("data:image/png;base64") || !encoded) throw new Error("PNG renderer returned invalid data");
  const binary = atob(encoded);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

export interface DashboardPNGComponent {
  element: HTMLElement;
  filename: string;
  title: string;
}

export function collectDashboardPNGComponents(element: HTMLElement): DashboardPNGComponent[] {
  const selector = [
    ".energy-history-summary", ".energy-history-provenance", ".thermal-dashboard",
    ".energy-history-chart", ".utility-heatmap", ".utility-ranking", ".energy-history-table",
  ].join(",");
  return Array.from(element.querySelectorAll<HTMLElement>(selector))
    .filter((candidate) => !candidate.closest("[hidden]") && candidate.getAttribute("aria-hidden") !== "true")
    .map((candidate, index) => {
      const title = candidate.getAttribute("aria-label")
        ?? candidate.querySelector("h2")?.textContent?.trim()
        ?? `Dashboard component ${index + 1}`;
      const slug = title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/(^-|-$)/g, "") || `component-${index + 1}`;
      return { element: candidate, filename: `${String(index + 1).padStart(2, "0")}-${slug}.png`, title };
    });
}

export async function exportDashboardPNG(element: HTMLElement, filename: string): Promise<void> {
  const width = 1_920;
  const height = 1_080;
  const files: Record<string, Uint8Array> = {};
  const components = collectDashboardPNGComponents(element);
  if (!components.length) throw new Error("Dashboard has no exportable components");

  try {
    for (const component of components) {
      const frame = document.createElement("section");
      frame.className = "dashboard-png-frame";
      frame.setAttribute("aria-hidden", "true");
      const header = document.createElement("header");
      const eyebrow = document.createElement("span");
      eyebrow.textContent = "ENERGY MANAGEMENT PLUGIN · DASHBOARD EXPORT";
      const heading = document.createElement("h1");
      heading.textContent = component.title;
      header.append(eyebrow, heading);
      const content = document.createElement("div");
      content.className = "dashboard-png-component";
      const clone = component.element.cloneNode(true) as HTMLElement;
      const bounds = component.element.getBoundingClientRect();
      const sourceWidth = Math.max(bounds.width, 1);
      const sourceHeight = Math.max(bounds.height, 1);
      const scale = Math.min(1_816 / sourceWidth, 820 / sourceHeight);
      Object.assign(clone.style, {
        flex: "none", width: `${sourceWidth}px`, height: `${sourceHeight}px`,
        margin: "0", transform: `scale(${scale})`, transformOrigin: "center",
      });
      content.append(clone);
      frame.append(header, content);
      document.body.append(frame);
      try {
        const dataURL = await toPng(frame, {
          backgroundColor: "#071019", cacheBust: true, pixelRatio: 1, width, height,
          canvasWidth: width, canvasHeight: height,
        });
        files[component.filename] = pngDataURLBytes(dataURL);
      } finally {
        frame.remove();
      }
    }
    downloadBlob(new Blob([zipSync(files)], { type: "application/zip" }), filename.replace(/\.png$/i, "-components.zip"));
  } finally {
    document.querySelectorAll(".dashboard-png-frame").forEach((frame) => frame.remove());
  }
}
