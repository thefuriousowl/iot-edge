import { AxiosHeaders, type AxiosRequestConfig, type AxiosResponse } from "axios";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import api from "../services/api";
import {
  checkSetupStatus,
  getMe,
  refreshAccessToken,
} from "../services/auth.service";
import type { AuthUser } from "../types/auth";
import { useAuthStore } from "./auth.store";

vi.mock("../services/auth.service", () => ({
  checkSetupStatus: vi.fn(),
  getMe: vi.fn(),
  refreshAccessToken: vi.fn(),
}));

const mockedCheckSetupStatus = vi.mocked(checkSetupStatus);
const mockedGetMe = vi.mocked(getMe);
const mockedRefreshAccessToken = vi.mocked(refreshAccessToken);

const user: AuthUser = {
  id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
  username: "admin",
  created_at: "2026-08-21T08:00:00Z",
  last_login: "2026-08-21T09:00:00Z",
};

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

describe("auth store", () => {
  beforeEach(() => {
    useAuthStore.getState().clearSession();
    useAuthStore.setState({ setupRequired: null });
    mockedCheckSetupStatus.mockReset();
    mockedCheckSetupStatus.mockResolvedValue({ setup_required: false });
    mockedGetMe.mockReset();
    mockedRefreshAccessToken.mockReset();
  });

  afterEach(() => {
    useAuthStore.getState().clearSession();
    vi.useRealTimers();
  });

  it("starts with an anonymous in-memory session", () => {
    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isInitialized: true,
      isLoading: false,
      setupRequired: null,
    });
  });

  it("stores a session and supplies its access token to the API client", async () => {
    useAuthStore.getState().setSession(user, "initial-access-token");

    expect(useAuthStore.getState()).toMatchObject({
      user,
      accessToken: "initial-access-token",
      isAuthenticated: true,
      isLoading: false,
    });

    const response = await api.get("/auth/me", {
      adapter: successfulAdapter,
    });

    expect(response.config.headers.Authorization).toBe(
      "Bearer initial-access-token",
    );
  });

  it("updates the access token without replacing the current user", async () => {
    useAuthStore.getState().setSession(user, "initial-access-token");

    useAuthStore.getState().setAccessToken("refreshed-access-token");

    expect(useAuthStore.getState()).toMatchObject({
      user,
      accessToken: "refreshed-access-token",
      isAuthenticated: true,
    });

    const response = await api.get("/auth/me", {
      adapter: successfulAdapter,
    });

    expect(response.config.headers.Authorization).toBe(
      "Bearer refreshed-access-token",
    );
  });

  it("tracks loading state independently from the current session", () => {
    useAuthStore.getState().setLoading(true);

    expect(useAuthStore.getState().isLoading).toBe(true);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it("clears the session and removes authorization from the API client", async () => {
    useAuthStore.getState().setSession(user, "initial-access-token");

    useAuthStore.getState().clearSession();

    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isLoading: false,
    });

    const response = await api.get("/auth/me", {
      adapter: successfulAdapter,
    });

    expect(response.config.headers.Authorization).toBeUndefined();
  });

  it("refreshes the access token before it expires and reschedules", async () => {
    vi.useFakeTimers();
    mockedRefreshAccessToken.mockResolvedValue({
      access_token: "proactively-refreshed-token",
      expires_in: 900,
    });
    useAuthStore.getState().setSession(user, "initial-access-token", 900);

    await vi.advanceTimersByTimeAsync(839_999);
    expect(mockedRefreshAccessToken).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);

    expect(mockedRefreshAccessToken).toHaveBeenCalledOnce();
    expect(useAuthStore.getState().accessToken).toBe(
      "proactively-refreshed-token",
    );
  });

  it("cancels proactive refresh when the session is cleared", async () => {
    vi.useFakeTimers();
    useAuthStore.getState().setSession(user, "initial-access-token", 900);

    useAuthStore.getState().clearSession();
    await vi.advanceTimersByTimeAsync(840_000);

    expect(mockedRefreshAccessToken).not.toHaveBeenCalled();
  });

  it("restores a session from the refresh cookie", async () => {
    mockedRefreshAccessToken.mockResolvedValue({
      access_token: "restored-access-token",
      expires_in: 900,
    });
    mockedGetMe.mockResolvedValue(user);
    useAuthStore.setState({
      isInitialized: false,
      isLoading: false,
    });

    await useAuthStore.getState().initializeSession();

    expect(mockedRefreshAccessToken).toHaveBeenCalledOnce();
    expect(mockedCheckSetupStatus).toHaveBeenCalledOnce();
    expect(mockedGetMe).toHaveBeenCalledOnce();
    expect(useAuthStore.getState()).toMatchObject({
      user,
      accessToken: "restored-access-token",
      isAuthenticated: true,
      isInitialized: true,
      isLoading: false,
    });
  });

  it("shares one initialization attempt in React Strict Mode", async () => {
    let resolveRefresh!: (response: {
      access_token: string;
      expires_in: number;
    }) => void;
    mockedRefreshAccessToken.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    mockedGetMe.mockResolvedValue(user);
    useAuthStore.setState({
      isInitialized: false,
      isLoading: false,
    });

    const firstInitialization = useAuthStore.getState().initializeSession();
    const secondInitialization = useAuthStore.getState().initializeSession();

    await vi.waitFor(() => {
      expect(mockedRefreshAccessToken).toHaveBeenCalledOnce();
    });
    resolveRefresh({ access_token: "restored-access-token", expires_in: 900 });
    await Promise.all([firstInitialization, secondInitialization]);

    expect(mockedGetMe).toHaveBeenCalledOnce();
  });

  it("finishes anonymously when session restoration fails", async () => {
    mockedRefreshAccessToken.mockRejectedValue(new Error("session expired"));
    useAuthStore.setState({
      isInitialized: false,
      isLoading: false,
    });

    await useAuthStore.getState().initializeSession();

    expect(mockedGetMe).not.toHaveBeenCalled();
    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isInitialized: true,
      isLoading: false,
    });
  });

  it("stops at initial setup without attempting session refresh", async () => {
    mockedCheckSetupStatus.mockResolvedValue({ setup_required: true });
    useAuthStore.setState({
      isInitialized: false,
      isLoading: false,
    });

    await useAuthStore.getState().initializeSession();

    expect(mockedRefreshAccessToken).not.toHaveBeenCalled();
    expect(mockedGetMe).not.toHaveBeenCalled();
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: false,
      isInitialized: true,
      isLoading: false,
      setupRequired: true,
    });
  });

  it("marks setup complete without authenticating the new account", () => {
    useAuthStore.setState({ setupRequired: true });

    useAuthStore.getState().markSetupComplete();

    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: false,
      setupRequired: false,
    });
  });
});
