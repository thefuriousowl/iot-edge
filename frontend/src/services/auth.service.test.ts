import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AuthUser,
  LoginRequest,
  LoginResponse,
  LogoutResponse,
  RefreshResponse,
  SetupRequest,
  SetupResponse,
  SetupStatusResponse,
} from "../types/auth";
import api from "./api";
import {
  checkSetupStatus,
  getMe,
  login,
  logout,
  refreshAccessToken,
  setup,
} from "./auth.service";

vi.mock("./api", () => ({
  default: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);

describe("auth service", () => {
  beforeEach(() => {
    mockedGet.mockReset();
    mockedPost.mockReset();
  });

  it("checks whether initial setup is required", async () => {
    const expected: SetupStatusResponse = { setup_required: true };
    mockedGet.mockResolvedValue({
      data: expected,
    } as AxiosResponse<SetupStatusResponse>);

    await expect(checkSetupStatus()).resolves.toEqual(expected);

    expect(mockedGet).toHaveBeenCalledOnce();
    expect(mockedGet).toHaveBeenCalledWith("/auth/setup/status");
  });

  it("submits the initial account setup", async () => {
    const request: SetupRequest = {
      username: "admin",
      password: "SecureP@ss123",
      confirm_password: "SecureP@ss123",
    };
    const expected: SetupResponse = {
      message: "Setup completed successfully",
      user: {
        id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
        username: "admin",
        created_at: "2026-08-21T08:00:00Z",
      },
    };
    mockedPost.mockResolvedValue({
      data: expected,
    } as AxiosResponse<SetupResponse>);

    await expect(setup(request)).resolves.toEqual(expected);

    expect(mockedPost).toHaveBeenCalledOnce();
    expect(mockedPost).toHaveBeenCalledWith("/auth/setup", request);
  });

  it("submits credentials and returns the login token response", async () => {
    const request: LoginRequest = {
      username: "admin",
      password: "SecureP@ss123",
    };
    const expected: LoginResponse = {
      access_token: "test-access-token",
      token_type: "Bearer",
      expires_in: 900,
    };
    mockedPost.mockResolvedValue({
      data: expected,
    } as AxiosResponse<LoginResponse>);

    await expect(login(request)).resolves.toEqual(expected);

    expect(mockedPost).toHaveBeenCalledOnce();
    expect(mockedPost).toHaveBeenCalledWith("/auth/login", request);
  });

  it("refreshes the access token using the refresh cookie", async () => {
    const expected: RefreshResponse = {
      access_token: "refreshed-access-token",
      expires_in: 900,
    };
    mockedPost.mockResolvedValue({
      data: expected,
    } as AxiosResponse<RefreshResponse>);

    await expect(refreshAccessToken()).resolves.toEqual(expected);

    expect(mockedPost).toHaveBeenCalledOnce();
    expect(mockedPost).toHaveBeenCalledWith("/auth/refresh");
  });

  it("logs out the authenticated session", async () => {
    const expected: LogoutResponse = {
      message: "Logged out successfully",
    };
    mockedPost.mockResolvedValue({
      data: expected,
    } as AxiosResponse<LogoutResponse>);

    await expect(logout()).resolves.toEqual(expected);

    expect(mockedPost).toHaveBeenCalledOnce();
    expect(mockedPost).toHaveBeenCalledWith("/auth/logout");
  });

  it("gets the authenticated user", async () => {
    const expected: AuthUser = {
      id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
      username: "admin",
      created_at: "2026-08-21T08:00:00Z",
      last_login: "2026-08-21T09:00:00Z",
    };
    mockedGet.mockResolvedValue({
      data: expected,
    } as AxiosResponse<AuthUser>);

    await expect(getMe()).resolves.toEqual(expected);

    expect(mockedGet).toHaveBeenCalledOnce();
    expect(mockedGet).toHaveBeenCalledWith("/auth/me");
  });

  it("does not hide API errors from callers", async () => {
    const expectedError = new Error("Invalid credentials");
    mockedPost.mockRejectedValue(expectedError);

    await expect(
      login({ username: "admin", password: "wrong-password" }),
    ).rejects.toBe(expectedError);
  });
});
