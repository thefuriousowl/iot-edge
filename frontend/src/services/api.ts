import axios, { type AxiosError, type InternalAxiosRequestConfig } from "axios";

import type { RefreshResponse } from "../types/auth";

let accessToken: string | null = null;
let refreshPromise: Promise<RefreshResponse> | null = null;

interface AuthSessionHandlers {
  refreshAccessToken: () => Promise<RefreshResponse>;
  onTokenRefreshed: (response: RefreshResponse) => void;
  onSessionExpired: () => void;
}

type RetryableRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean;
};

const authEndpointsWithoutRefresh = new Set([
  "/auth/login",
  "/auth/refresh",
  "/auth/setup",
  "/auth/setup/status",
]);

export function setAccessToken(token: string): void {
  accessToken = token;
}

export function clearAccessToken(): void {
  accessToken = null;
}

const api = axios.create({
  baseURL: import.meta.env.VITE_API_URL || "/api",
  headers: {
    "Content-Type": "application/json",
  },
  withCredentials: true,
});

const defaultAuthSessionHandlers: AuthSessionHandlers = {
  refreshAccessToken: async () => {
    const response = await api.post<RefreshResponse>("/auth/refresh");

    return response.data;
  },
  onTokenRefreshed: (response) => {
    setAccessToken(response.access_token);
  },
  onSessionExpired: clearAccessToken,
};

let authSessionHandlers = defaultAuthSessionHandlers;

export function configureAuthSessionHandlers(
  handlers: Partial<AuthSessionHandlers>,
): () => void {
  const previousHandlers = authSessionHandlers;
  authSessionHandlers = {
    ...defaultAuthSessionHandlers,
    ...handlers,
  };

  return () => {
    authSessionHandlers = previousHandlers;
  };
}

function isRefreshableUnauthorizedError(
  error: unknown,
): error is AxiosError & { config: RetryableRequestConfig } {
  if (!axios.isAxiosError(error) || error.response?.status !== 401) {
    return false;
  }

  const requestConfig = error.config as RetryableRequestConfig | undefined;

  return Boolean(
    requestConfig &&
      !requestConfig._retry &&
      !authEndpointsWithoutRefresh.has(requestConfig.url ?? ""),
  );
}

async function refreshAccessTokenOnce(): Promise<RefreshResponse> {
  if (!refreshPromise) {
    refreshPromise = authSessionHandlers
      .refreshAccessToken()
      .then((response) => {
        setAccessToken(response.access_token);
        authSessionHandlers.onTokenRefreshed(response);

        return response;
      })
      .catch((error: unknown) => {
        clearAccessToken();
        authSessionHandlers.onSessionExpired();

        throw error;
      })
      .finally(() => {
        refreshPromise = null;
      });
  }

  return refreshPromise;
}

api.interceptors.request.use(
  (requestConfig) => {
    if (accessToken) {
      requestConfig.headers.set("Authorization", `Bearer ${accessToken}`);
    }

    return requestConfig;
  },
  (error: unknown) => Promise.reject(error),
);

api.interceptors.response.use(
  (response) => response,
  async (error: unknown) => {
    if (!isRefreshableUnauthorizedError(error)) {
      return Promise.reject(error);
    }

    const requestConfig = error.config;
    requestConfig._retry = true;

    try {
      const refreshed = await refreshAccessTokenOnce();
      requestConfig.headers.set(
        "Authorization",
        `Bearer ${refreshed.access_token}`,
      );

      return api.request(requestConfig);
    } catch (refreshError) {
      return Promise.reject(refreshError);
    }
  },
);

export default api;
