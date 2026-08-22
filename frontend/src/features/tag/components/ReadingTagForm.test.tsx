// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { listDatasources, listDevices } from "../../../services/device.service";
import { createTag, previewTag } from "../../../services/tag.service";
import { listVGateways } from "../../../services/vgateway.service";
import type { Datasource, Device } from "../../../types/device";
import type { ReadingTag, TagPreviewResult } from "../../../types/tag";
import type { VGatewayListItem, VGatewayListResponse } from "../../../types/vgateway";
import ReadingTagForm from "./ReadingTagForm";

vi.mock("../../../services/device.service", () => ({
  listDatasources: vi.fn(),
  listDevices: vi.fn(),
}));

vi.mock("../../../services/tag.service", () => ({
  createTag: vi.fn(),
  previewTag: vi.fn(),
}));

vi.mock("../../../services/vgateway.service", () => ({
  listVGateways: vi.fn(),
}));

const mockedListGateways = vi.mocked(listVGateways);
const mockedListDevices = vi.mocked(listDevices);
const mockedListDatasources = vi.mocked(listDatasources);
const mockedCreateTag = vi.mocked(createTag);
const mockedPreviewTag = vi.mocked(previewTag);

const gateway: VGatewayListItem = {
  id: "gateway-1",
  name: "Factory gateway",
  type: "modbus_tcp",
  description: null,
  enabled: true,
  status: "connected",
  device_count: 1,
  last_activity: null,
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const device: Device = {
  id: "device-1",
  vgateway_id: gateway.id,
  name: "Main meter",
  type: "modbus_device",
  description: null,
  enabled: true,
  config: { unit_id: 1, poll_interval_ms: 1000, request_timeout_ms: null },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const datasource: Datasource = {
  id: "datasource-1",
  device_id: device.id,
  name: "Holding registers",
  type: "modbus_read",
  description: null,
  enabled: true,
  status: "idle",
  config: { function_code: 3, start_address: 0, quantity: 10, poll_interval_ms: null },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const createdTag: ReadingTag = {
  id: "tag-1",
  datasource_id: datasource.id,
  name: "line_voltage_l1",
  type: "reading",
  data_type: "float32",
  description: "Line voltage",
  enabled: true,
  config: { decoder: { type: "binary_numeric", config: { byte_offset: 2, byte_order: "little_endian", bit_offset: 0 } } },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const previewResult: TagPreviewResult = {
  tag_id: "00000000-0000-0000-0000-000000000000",
  observed_at: "2026-08-22T04:00:00Z",
  quality: "good",
  data_type: "uint16",
  value: 1111,
};

function gatewayResponse(data: VGatewayListItem[] = [gateway]): VGatewayListResponse {
  return { data, pagination: { page: 1, per_page: 100, total: data.length, total_pages: data.length ? 1 : 0 } };
}

function renderForm(overrides?: Partial<{ onCancel: () => void; onChangeType: () => void; onCreated: () => void }>) {
  const props = {
    onCancel: vi.fn(),
    onChangeType: vi.fn(),
    onCreated: vi.fn(),
    ...overrides,
  };
  render(
    <MemoryRouter>
      <ReadingTagForm {...props} />
    </MemoryRouter>,
  );
  return props;
}

async function selectDatasourceHierarchy() {
  await screen.findByRole("option", { name: "Factory gateway · Modbus Tcp" });
  fireEvent.change(screen.getByLabelText("vGateway"), { target: { value: gateway.id } });
  await screen.findByRole("option", { name: "Main meter · Modbus Device" });
  fireEvent.change(screen.getByLabelText("Device"), { target: { value: device.id } });
  await screen.findByRole("option", { name: "Holding registers · Modbus Read" });
  fireEvent.change(screen.getByLabelText("Datasource"), { target: { value: datasource.id } });
}

describe("ReadingTagForm", () => {
  beforeEach(() => {
    mockedListGateways.mockReset();
    mockedListDevices.mockReset();
    mockedListDatasources.mockReset();
    mockedCreateTag.mockReset();
    mockedPreviewTag.mockReset();
    mockedListGateways.mockResolvedValue(gatewayResponse());
    mockedListDevices.mockResolvedValue([device]);
    mockedListDatasources.mockResolvedValue([datasource]);
  });

  afterEach(cleanup);

  it("loads the hierarchy progressively and creates an exact typed Reading Tag", async () => {
    const onCreated = vi.fn();
    mockedCreateTag.mockResolvedValue(createdTag);
    renderForm({ onCreated });

    expect(screen.getByLabelText("Device")).toBeDisabled();
    expect(screen.getByLabelText("Datasource")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Create Reading Tag" })).toBeDisabled();
    await selectDatasourceHierarchy();

    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: " line_voltage_l1 " } });
    fireEvent.change(screen.getByLabelText(/Data type/), { target: { value: "float32" } });
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: " Line voltage " } });
    fireEvent.change(screen.getByLabelText(/Byte offset/), { target: { value: "2" } });
    fireEvent.change(screen.getByLabelText(/Byte order/), { target: { value: "little_endian" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Reading Tag" }));

    await waitFor(() => expect(mockedCreateTag).toHaveBeenCalledWith({
      name: "line_voltage_l1",
      type: "reading",
      data_type: "float32",
      datasource_id: datasource.id,
      description: "Line voltage",
      enabled: true,
      config: { decoder: { type: "binary_numeric", config: { byte_offset: 2, byte_order: "little_endian" } } },
    }));
    expect(onCreated).toHaveBeenCalledOnce();
    expect(mockedListGateways).toHaveBeenCalledWith({ page: 1, per_page: 100 }, expect.any(AbortSignal));
    expect(mockedListDevices).toHaveBeenCalledWith(gateway.id, expect.any(AbortSignal));
    expect(mockedListDatasources).toHaveBeenCalledWith(device.id, expect.any(AbortSignal));
  });

  it("shows bit offset only for bool and includes it in decoder config", async () => {
    mockedCreateTag.mockResolvedValue({ ...createdTag, data_type: "bool" });
    renderForm();
    await selectDatasourceHierarchy();

    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: "breaker_closed" } });
    fireEvent.change(screen.getByLabelText(/Data type/), { target: { value: "bool" } });
    fireEvent.change(screen.getByLabelText(/Bit offset/), { target: { value: "3" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Reading Tag" }));

    await waitFor(() => expect(mockedCreateTag).toHaveBeenCalledWith(expect.objectContaining({
      data_type: "bool",
      config: { decoder: { type: "binary_numeric", config: { byte_offset: 0, byte_order: "big_endian", bit_offset: 3 } } },
    })));
    fireEvent.change(screen.getByLabelText(/Data type/), { target: { value: "uint16" } });
    expect(screen.queryByLabelText(/Bit offset/)).not.toBeInTheDocument();
  });

  it("shows industrial byte-order notation and explains register-to-byte offsets", async () => {
    renderForm();

    expect(screen.getByRole("option", { name: "Big endian (ABCD)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Word swap (CDAB)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Byte swap (BADC)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Little endian (DCBA)" })).toBeInTheDocument();
    expect(screen.getByText(/register offsets 0, 2, 4, and 6 map to byte offsets 0, 4, 8, and 12/i)).toBeInTheDocument();
  });

  it("previews without a Tag name and sends the exact unsaved decoder request", async () => {
    mockedPreviewTag.mockResolvedValue(previewResult);
    renderForm();
    await selectDatasourceHierarchy();

    fireEvent.change(screen.getByLabelText(/Byte offset/), { target: { value: "2" } });
    fireEvent.change(screen.getByLabelText(/Byte order/), { target: { value: "little_endian" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));

    await waitFor(() => expect(mockedPreviewTag).toHaveBeenCalledWith({
      type: "reading",
      datasource_id: datasource.id,
      data_type: "uint16",
      config: { decoder: { type: "binary_numeric", config: { byte_offset: 2, byte_order: "little_endian" } } },
    }));
    const result = await screen.findByLabelText("Tag preview result");
    expect(result).toHaveTextContent("1111");
    expect(result).toHaveTextContent("uint16 · good");
    expect(mockedCreateTag).not.toHaveBeenCalled();
  });

  it("clears stale preview state and blocks invalid decoder ranges", async () => {
    mockedPreviewTag.mockResolvedValueOnce(previewResult).mockRejectedValueOnce({
      isAxiosError: true,
      response: { data: { error: { code: "TAG007", message: "Modbus exception 0x02: Illegal Data Address. The requested address range 99-100 is not available on the server." } } },
    });
    renderForm();
    await selectDatasourceHierarchy();

    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    expect(await screen.findByText("1111")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Byte offset/), { target: { value: "-1" } });
    expect(screen.queryByLabelText("Tag preview result")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Tag preview")).toHaveTextContent("Read and decode");

    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    expect(mockedPreviewTag).toHaveBeenCalledOnce();

    fireEvent.change(screen.getByLabelText(/Byte offset/), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Modbus exception 0x02: Illegal Data Address");
    expect(screen.getByRole("alert")).toHaveTextContent("address range 99-100");
    expect(screen.queryByLabelText("Tag preview result")).not.toBeInTheDocument();
  });

  it("aborts stale device loads and resets downstream selections", async () => {
    const secondGateway = { ...gateway, id: "gateway-2", name: "Backup gateway" };
    mockedListGateways.mockResolvedValue(gatewayResponse([gateway, secondGateway]));
    mockedListDevices.mockImplementation((gatewayID) => gatewayID === secondGateway.id ? Promise.resolve([{ ...device, id: "device-2", vgateway_id: secondGateway.id, name: "Backup meter" }]) : new Promise(() => {}));
    renderForm();

    await screen.findByRole("option", { name: "Factory gateway · Modbus Tcp" });
    fireEvent.change(screen.getByLabelText("vGateway"), { target: { value: gateway.id } });
    await waitFor(() => expect(mockedListDevices).toHaveBeenCalledOnce());
    const firstSignal = mockedListDevices.mock.calls[0][1];
    fireEvent.change(screen.getByLabelText("vGateway"), { target: { value: secondGateway.id } });

    expect(await screen.findByRole("option", { name: "Backup meter · Modbus Device" })).toBeInTheDocument();
    expect(firstSignal?.aborted).toBe(true);
    expect(screen.getByLabelText("Device")).toHaveValue("");
    expect(screen.getByLabelText("Datasource")).toBeDisabled();
  });

  it("retries only the failed hierarchy level", async () => {
    mockedListDevices.mockRejectedValueOnce(new Error("device API unavailable")).mockResolvedValueOnce([device]);
    renderForm();

    await screen.findByRole("option", { name: "Factory gateway · Modbus Tcp" });
    fireEvent.change(screen.getByLabelText("vGateway"), { target: { value: gateway.id } });
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load devices");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByRole("option", { name: "Main meter · Modbus Device" })).toBeInTheDocument();
    expect(mockedListGateways).toHaveBeenCalledOnce();
    expect(mockedListDevices).toHaveBeenCalledTimes(2);
  });

  it("shows API creation errors, allows dismissal, and preserves navigation controls", async () => {
    const onCancel = vi.fn();
    const onChangeType = vi.fn();
    mockedCreateTag.mockRejectedValue({ isAxiosError: true, response: { data: { error: { message: "Tag name already exists" } } } });
    renderForm({ onCancel, onChangeType });
    await selectDatasourceHierarchy();
    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: "duplicate" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Reading Tag" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Tag name already exists");
    fireEvent.click(screen.getByRole("button", { name: "Dismiss error" }));
    expect(screen.queryByText("Tag name already exists")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Change type" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onChangeType).toHaveBeenCalledOnce();
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("links to gateway setup when no source hierarchy exists", async () => {
    mockedListGateways.mockResolvedValue(gatewayResponse([]));
    renderForm();

    expect(await screen.findByText("No vGateways are configured.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Add a vGateway first." })).toHaveAttribute("href", "/vgateways/new");
  });
});
