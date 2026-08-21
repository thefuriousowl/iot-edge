import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  CreateVGatewayRequest,
  TestVGatewayConfigRequest,
  TestVGatewayConnectionResponse,
  UpdateVGatewayRequest,
  VGateway,
  VGatewayConnectionActionResponse,
  VGatewayDetail,
  VGatewayListResponse,
  VGatewayStatusResponse,
} from "../types/vgateway";
import api from "./api";
import {
  connectVGateway,
  createVGateway,
  deleteVGateway,
  disconnectVGateway,
  getVGateway,
  getVGatewayStatus,
  listVGateways,
  testVGatewayConfig,
  testVGatewayConnection,
  updateVGateway,
} from "./vgateway.service";

vi.mock("./api", () => ({
  default: {
    delete: vi.fn(),
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
}));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);

const gateway: VGateway = {
  id: "550e8400-e29b-41d4-a716-446655440000",
  name: "Main PLC Gateway",
  type: "modbus_tcp",
  description: "Connection to main factory PLC",
  enabled: true,
  status: "disconnected",
  config: {
    host: "192.168.1.100",
    port: 502,
    timeout: 5_000,
    retry_count: 3,
    retry_delay: 1_000,
    keep_alive: true,
    reconnect_interval: 30,
  },
  created_at: "2026-08-21T08:00:00Z",
  updated_at: "2026-08-21T08:00:00Z",
};

function responseWith<T>(data: T): AxiosResponse<T> {
  return { data } as AxiosResponse<T>;
}

describe("vGateway service", () => {
  beforeEach(() => {
    mockedDelete.mockReset();
    mockedGet.mockReset();
    mockedPost.mockReset();
    mockedPut.mockReset();
  });

  it("lists gateways with filters, pagination, and cancellation", async () => {
    const controller = new AbortController();
    const expected: VGatewayListResponse = {
      data: [
        {
          ...gateway,
          device_count: 3,
        },
      ],
      pagination: {
        page: 2,
        per_page: 20,
        total: 21,
        total_pages: 2,
      },
    };
    const params = {
      type: "modbus_tcp" as const,
      enabled: true,
      page: 2,
      per_page: 20,
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(
      listVGateways(params, controller.signal),
    ).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/vgateways", {
      params,
      signal: controller.signal,
    });
  });

  it("creates a ModbusTCP gateway", async () => {
    const request: CreateVGatewayRequest = {
      name: gateway.name,
      type: "modbus_tcp",
      description: gateway.description,
      enabled: gateway.enabled,
      config: gateway.config,
    };
    mockedPost.mockResolvedValue(responseWith(gateway));

    await expect(createVGateway(request)).resolves.toEqual(gateway);
    expect(mockedPost).toHaveBeenCalledWith("/vgateways", request);
  });

  it("gets gateway details with cancellation", async () => {
    const controller = new AbortController();
    const expected: VGatewayDetail = {
      ...gateway,
      devices: [
        {
          id: "660e8400-e29b-41d4-a716-446655440000",
          name: "Power Meter #1",
          unit_id: 1,
        },
      ],
      statistics: {
        connected_at: null,
        request_count: 12_345,
        error_count: 5,
        avg_latency_ms: 12.5,
      },
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(
      getVGateway(gateway.id, controller.signal),
    ).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith(`/vgateways/${gateway.id}`, {
      signal: controller.signal,
    });
  });

  it("updates a gateway without allowing its type to change", async () => {
    const request: UpdateVGatewayRequest = {
      name: "Updated PLC Gateway",
      enabled: false,
      config: {
        host: "192.168.1.101",
        port: 502,
      },
    };
    const expected: VGateway = {
      ...gateway,
      name: request.name ?? gateway.name,
      enabled: request.enabled ?? gateway.enabled,
      config: {
        ...gateway.config,
        ...request.config,
      },
    };
    mockedPut.mockResolvedValue(responseWith(expected));

    await expect(updateVGateway(gateway.id, request)).resolves.toEqual(expected);
    expect(mockedPut).toHaveBeenCalledWith(`/vgateways/${gateway.id}`, request);
  });

  it("deletes a gateway", async () => {
    mockedDelete.mockResolvedValue(responseWith(undefined));

    await expect(deleteVGateway(gateway.id)).resolves.toBeUndefined();
    expect(mockedDelete).toHaveBeenCalledWith(`/vgateways/${gateway.id}`);
  });

  it.each([
    ["connect", connectVGateway, "connected"],
    ["disconnect", disconnectVGateway, "disconnected"],
  ] as const)("runs the %s action", async (action, runAction, status) => {
    const expected: VGatewayConnectionActionResponse = {
      message: `${action} completed`,
      status,
    };
    mockedPost.mockResolvedValue(responseWith(expected));

    await expect(runAction(gateway.id)).resolves.toEqual(expected);
    expect(mockedPost).toHaveBeenCalledWith(
      `/vgateways/${gateway.id}/${action}`,
    );
  });

  it("tests a saved gateway connection with an optional unit ID", async () => {
    const expected: TestVGatewayConnectionResponse = {
      success: true,
      latency_ms: 15,
      message: "Connection successful",
    };
    mockedPost.mockResolvedValue(responseWith(expected));

    await expect(
      testVGatewayConnection(gateway.id, { unit_id: 1 }),
    ).resolves.toEqual(expected);
    expect(mockedPost).toHaveBeenCalledWith(
      `/vgateways/${gateway.id}/test`,
      { unit_id: 1 },
    );
  });

  it("tests unsaved gateway configuration", async () => {
    const request: TestVGatewayConfigRequest = {
      type: "modbus_tcp",
      config: gateway.config,
      options: { unit_id: 7 },
    };
    const expected: TestVGatewayConnectionResponse = {
      success: true,
      latency_ms: 12.5,
      message: "Connection successful",
    };
    mockedPost.mockResolvedValue(responseWith(expected));

    await expect(testVGatewayConfig(request)).resolves.toEqual(expected);
    expect(mockedPost).toHaveBeenCalledWith("/vgateways/test", request);
  });

  it("gets live gateway status with cancellation", async () => {
    const controller = new AbortController();
    const expected: VGatewayStatusResponse = {
      id: gateway.id,
      status: "connected",
      connected_at: "2026-08-21T08:00:00Z",
      last_activity: "2026-08-21T08:05:30Z",
      statistics: {
        request_count: 12_345,
        error_count: 5,
        bytes_received: 456_789,
        avg_latency_ms: 12.5,
      },
      health: {
        status: "healthy",
        last_check: "2026-08-21T08:05:00Z",
        latency_ms: 10,
      },
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(
      getVGatewayStatus(gateway.id, controller.signal),
    ).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith(
      `/vgateways/${gateway.id}/status`,
      { signal: controller.signal },
    );
  });

  it("does not hide API errors from callers", async () => {
    const expectedError = new Error("Gateway name already exists");
    mockedPost.mockRejectedValue(expectedError);

    await expect(
      createVGateway({
        name: gateway.name,
        type: "modbus_tcp",
        config: gateway.config,
      }),
    ).rejects.toBe(expectedError);
  });
});
