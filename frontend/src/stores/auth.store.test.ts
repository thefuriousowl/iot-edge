import { AxiosHeaders, type AxiosRequestConfig, type AxiosResponse } from "axios";
import { beforeEach, describe, expect, it } from "vitest";

import api from "../services/api";
import type { AuthUser } from "../types/auth";
import { useAuthStore } from "./auth.store";

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
  });

  it("starts with an anonymous in-memory session", () => {
    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isLoading: false,
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
});
