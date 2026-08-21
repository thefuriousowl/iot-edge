import { AxiosHeaders, type AxiosRequestConfig, type AxiosResponse } from "axios";
import { afterEach, describe, expect, it } from "vitest";

import api, { clearAccessToken, setAccessToken } from "./api";

function successfulAdapter(
  config: AxiosRequestConfig,
): Promise<AxiosResponse> {
  return Promise.resolve({
    config,
    data: {},
    headers: new AxiosHeaders(),
    status: 200,
    statusText: "OK",
  } as AxiosResponse);
}

describe("API client", () => {
  afterEach(() => {
    clearAccessToken();
  });

  it("uses the API URL from the Vite environment", () => {
    expect(api.defaults.baseURL).toBe(import.meta.env.VITE_API_URL);
  });

  it("sends cookies with cross-origin API requests", () => {
    expect(api.defaults.withCredentials).toBe(true);
  });

  it("adds the access token to the Authorization header", async () => {
    setAccessToken("test-access-token");

    const response = await api.get("/health", {
      adapter: successfulAdapter,
    });

    expect(response.config.headers.Authorization).toBe(
      "Bearer test-access-token",
    );
  });

  it("does not add an Authorization header after the token is cleared", async () => {
    setAccessToken("test-access-token");
    clearAccessToken();

    const response = await api.get("/health", {
      adapter: successfulAdapter,
    });

    expect(response.config.headers.Authorization).toBeUndefined();
  });

  it("forwards response errors to the caller", async () => {
    const expectedError = new Error("request failed");

    await expect(
      api.get("/health", {
        adapter: () => Promise.reject(expectedError),
      }),
    ).rejects.toBe(expectedError);
  });
});
