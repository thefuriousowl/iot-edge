import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import api from "./api";
import {
  getHealth,
  getInternetStatus,
  type HealthResponse,
  type InternetStatusResponse,
} from "./health.service";

vi.mock("./api", () => ({
  default: {
    get: vi.fn(),
  },
}));

const mockedGet = vi.mocked(api.get);

function responseWith<T>(data: T): AxiosResponse<T> {
  return { data } as AxiosResponse<T>;
}

describe("health service", () => {
  beforeEach(() => {
    mockedGet.mockReset();
  });

  it("gets backend health with cancellation", async () => {
    const controller = new AbortController();
    const expected: HealthResponse = { status: "ok", version: "0.1.0" };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(getHealth(controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/health", {
      signal: controller.signal,
    });
  });

  it("gets authenticated internet status with cancellation", async () => {
    const controller = new AbortController();
    const expected: InternetStatusResponse = {
      status: "online",
      checked_at: "2026-08-21T06:00:00Z",
      latency_ms: 12.5,
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(getInternetStatus(controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/system/internet-status", {
      signal: controller.signal,
    });
  });

  it("does not hide internet status API failures", async () => {
    const expectedError = new Error("backend unavailable");
    mockedGet.mockRejectedValue(expectedError);

    await expect(getInternetStatus()).rejects.toBe(expectedError);
  });
});
