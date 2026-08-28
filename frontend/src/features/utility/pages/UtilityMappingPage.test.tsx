// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { listAssets } from "../../../services/asset.service";
import { getDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import UtilityMappingPage from "./UtilityMappingPage";
import { buildCalculationPreview, validateUtilityDraft } from "./utilityMapping";
vi.mock("../../../services/asset.service", () => ({ listAssets: vi.fn() }));
vi.mock("../../../services/datalogger.service", () => ({ listDataLoggers: vi.fn(), getDataLogger: vi.fn() }));
afterEach(() => cleanup());
it("guides a user from resource and Asset to a validated Tag mapping", async () => {
  vi.mocked(listAssets).mockResolvedValue({ data: [{ id: "a1", parent_id: null, name: "Plant", kind: "site", description: null, enabled: true, timezone: "UTC", position: 0, metadata: {}, created_at: "", updated_at: "" }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
  const logger = { id: "l1", name: "Plant history", description: null, enabled: true, timezone: "UTC", mode: "interval" as const, start_at: "", end_at: null, max_size_bytes: null, config: { interval_seconds: 60 }, tag_count: 1, tags: [{ id: "t1", name: "Air flow", type: "reading" as const, data_type: "float64" as const, enabled: true }], created_at: "", updated_at: "" };
  vi.mocked(listDataLoggers).mockResolvedValue({ data: [logger], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } }); vi.mocked(getDataLogger).mockResolvedValue(logger);
  render(<MemoryRouter><UtilityMappingPage /></MemoryRouter>); await screen.findByRole("heading", { name: "1. Choose the utility boundary" });
  fireEvent.change(screen.getByLabelText("Resource template"), { target: { value: "compressed_air" } }); fireEvent.change(screen.getByLabelText("Asset"), { target: { value: "a1" } }); fireEvent.change(screen.getByLabelText("Data Logger"), { target: { value: "l1" } });
  await waitFor(() => expect(screen.getByLabelText("Logger Tag")).toContainHTML("Air flow")); fireEvent.change(screen.getByLabelText("Logger Tag"), { target: { value: "t1" } }); fireEvent.change(screen.getByLabelText("Measurement"), { target: { value: "flow rate" } }); fireEvent.change(screen.getByLabelText("Unit"), { target: { value: "Nm3/h" } });
  fireEvent.change(screen.getByLabelText("Compressor power Tag"), { target: { value: "t1" } });
  expect(screen.getByRole("region", { name: "Calculation preview" })).toHaveTextContent("Compressor kWh ÷ normalized air Nm³ = kWh/Nm³"); expect(screen.getByRole("region", { name: "Calculation preview" })).toHaveTextContent("committed Data Logger batches"); expect(screen.getByRole("region", { name: "Calculation preview" })).toHaveTextContent("Nm3/h already matches the calculation unit");
  const review = screen.getByRole("button", { name: "Review mapping" }); expect(review).toBeEnabled(); fireEvent.click(review); expect(screen.getByRole("heading", { name: "Ready for backend validation" })).toBeInTheDocument();
});
it("returns actionable validation messages", () => { expect(validateUtilityDraft({ assetID: "", loggerID: "", resource: "electricity", sourceKind: "plugin_output", sourceID: "", quantity: "pressure", unit: "bar", tariffMode: "fixed", currency: "th", rate: -1 })).toHaveLength(7); });
it("fails closed when compressed-air reference, power, pressure, and baseline evidence are missing", () => { const errors = validateUtilityDraft({ assetID: "a", loggerID: "l", resource: "compressed_air", sourceKind: "tag", sourceID: "flow", quantity: "flow rate", unit: "Nm3/h", tariffMode: "none", currency: "THB", rate: 0, flowBasis: "normalized", referenceTemperatureKelvin: 0, referencePressurePascal: 0 }); expect(errors).toEqual(expect.arrayContaining(["Choose the compressor power Tag.", "Enter positive reference temperature and pressure for normalized air.", "Choose whether pressure is gauge or absolute.", "Choose an explicit idle or no-production baseline."])); });
it("previews conversion and the first fail-closed reason without fabricating coverage", () => { const preview = buildCalculationPreview({ assetID: "", loggerID: "", resource: "electricity", sourceKind: "tag", sourceID: "", quantity: "power", unit: "W", tariffMode: "none", currency: "THB", rate: 0 }); expect(preview.unitConversion).toBe("W → kW before calculation"); expect(preview.coverage).toMatch(/persisted segments/); expect(preview.failClosedReason).toBe("Choose the Asset that owns this utility boundary."); });
