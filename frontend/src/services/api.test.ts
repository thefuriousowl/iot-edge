import {
  AxiosError,
  AxiosHeaders,
  type AxiosRequestConfig,
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from "axios";
import { afterEach, describe, expect, it, vi } from "vitest";

import api, {
  clearAccessToken,
  configureAuthSessionHandlers,
  setAccessToken,
} from "./api";

const restoreAuthHandlers: Array<() => void> = [];

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

function unauthorizedError(
  config: InternalAxiosRequestConfig,
): AxiosError {
  const response = {
    config,
    data: {
      error: {
        code: "AUTH004",
        message: "Invalid token",
      },
    },
    headers: new AxiosHeaders(),
    status: 401,
    statusText: "Unauthorized",
  } as AxiosResponse;

  return new AxiosError(
    "Request failed with status code 401",
    AxiosError.ERR_BAD_REQUEST,
    config,
    undefined,
    response,
  );
}

function configureTestAuthHandlers(
  handlers: Parameters<typeof configureAuthSessionHandlers>[0],
) {
  restoreAuthHandlers.push(configureAuthSessionHandlers(handlers));
}

describe("API client", () => {
  afterEach(() => {
    while (restoreAuthHandlers.length > 0) {
      restoreAuthHandlers.pop()?.();
    }
    clearAccessToken();
    vi.restoreAllMocks();
  });

  it("uses the API URL from the Vite environment", () => {
    expect(api.defaults.baseURL).toBe(import.meta.env.VITE_API_URL || '/api');
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

  it("refreshes once and retries a protected request after a 401", async () => {
    const refreshAccessToken = vi.fn().mockResolvedValue({
      access_token: "refreshed-access-token",
      expires_in: 900,
    });
    const onTokenRefreshed = vi.fn();
    configureTestAuthHandlers({ refreshAccessToken, onTokenRefreshed });
    let requestCount = 0;

    const response = await api.get("/auth/me", {
      adapter: async (config) => {
        requestCount += 1;

        if (requestCount === 1) {
          throw unauthorizedError(config);
        }

        return successfulAdapter(config);
      },
    });

    expect(response.config.headers.Authorization).toBe(
      "Bearer refreshed-access-token",
    );
    expect(requestCount).toBe(2);
    expect(refreshAccessToken).toHaveBeenCalledOnce();
    expect(onTokenRefreshed).toHaveBeenCalledWith({
      access_token: "refreshed-access-token",
      expires_in: 900,
    });
  });

  it("shares one refresh request across concurrent 401 responses", async () => {
    let resolveRefresh!: (response: {
      access_token: string;
      expires_in: number;
    }) => void;
    const refreshAccessToken = vi.fn(
      () =>
        new Promise<{ access_token: string; expires_in: number }>((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    configureTestAuthHandlers({ refreshAccessToken });

    const adapter = async (config: InternalAxiosRequestConfig) => {
      if (config.headers.Authorization !== "Bearer concurrent-token") {
        throw unauthorizedError(config);
      }

      return successfulAdapter(config);
    };

    const firstRequest = api.get("/auth/me", { adapter });
    const secondRequest = api.get("/protected/resource", { adapter });

    await vi.waitFor(() => {
      expect(refreshAccessToken).toHaveBeenCalledOnce();
    });
    resolveRefresh({
      access_token: "concurrent-token",
      expires_in: 900,
    });

    const [firstResponse, secondResponse] = await Promise.all([
      firstRequest,
      secondRequest,
    ]);

    expect(firstResponse.status).toBe(200);
    expect(secondResponse.status).toBe(200);
    expect(refreshAccessToken).toHaveBeenCalledOnce();
  });

  it("does not refresh public authentication failures", async () => {
    const refreshAccessToken = vi.fn();
    configureTestAuthHandlers({ refreshAccessToken });

    await expect(
      api.post("/auth/login", undefined, {
        adapter: async (config) => {
          throw unauthorizedError(config);
        },
      }),
    ).rejects.toMatchObject({ response: { status: 401 } });

    expect(refreshAccessToken).not.toHaveBeenCalled();
  });

  it("clears the session when refreshing a protected request fails", async () => {
    const refreshError = new Error("refresh cookie expired");
    const onSessionExpired = vi.fn();
    configureTestAuthHandlers({
      refreshAccessToken: vi.fn().mockRejectedValue(refreshError),
      onSessionExpired,
    });

    await expect(
      api.get("/auth/me", {
        adapter: async (config) => {
          throw unauthorizedError(config);
        },
      }),
    ).rejects.toBe(refreshError);

    expect(onSessionExpired).toHaveBeenCalledOnce();
  });

  it("does not enter a refresh loop when the retried request is still unauthorized", async () => {
    const refreshAccessToken = vi.fn().mockResolvedValue({
      access_token: "refreshed-access-token",
      expires_in: 900,
    });
    configureTestAuthHandlers({ refreshAccessToken });
    let requestCount = 0;

    await expect(
      api.get("/auth/me", {
        adapter: async (config) => {
          requestCount += 1;
          throw unauthorizedError(config);
        },
      }),
    ).rejects.toMatchObject({ response: { status: 401 } });

    expect(requestCount).toBe(2);
    expect(refreshAccessToken).toHaveBeenCalledOnce();
  });
});
