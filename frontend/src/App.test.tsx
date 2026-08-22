// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "./App";
import {
  checkSetupStatus,
  getMe,
  login,
  refreshAccessToken,
  setup,
} from "./services/auth.service";
import { listDataLoggers } from "./services/datalogger.service";
import { listDeviceInventory } from "./services/device.service";
import { getHealth } from "./services/health.service";
import { listTags } from "./services/tag.service";
import { listVGateways } from "./services/vgateway.service";
import { useAuthStore } from "./stores/auth.store";
import type { AuthUser } from "./types/auth";

vi.mock("./services/health.service", () => ({
  getHealth: vi.fn(),
}));

vi.mock("./services/vgateway.service", () => ({ listVGateways: vi.fn() }));
vi.mock("./services/device.service", () => ({ listDeviceInventory: vi.fn() }));
vi.mock("./services/tag.service", () => ({ listTags: vi.fn() }));
vi.mock("./services/datalogger.service", () => ({ listDataLoggers: vi.fn() }));

vi.mock("./services/auth.service", () => ({
  checkSetupStatus: vi.fn(),
  getMe: vi.fn(),
  login: vi.fn(),
  refreshAccessToken: vi.fn(),
  setup: vi.fn(),
}));

vi.mock("./features/tag/pages/TagWizardPage", () => ({
  default: () => <h1>Add Tag Wizard</h1>,
}));

vi.mock("./features/tag/pages/TagDetailPage", () => ({
  default: () => <h1>Real-time Tag Monitor</h1>,
}));

vi.mock("./features/tag/components/TagLiveStream", () => ({
  default: () => null,
}));

vi.mock("./features/device/pages/DeviceListPage", () => ({
  default: () => <h1>Global Device Inventory</h1>,
}));

vi.mock("./features/datalogger/pages/DataLoggerListPage", () => ({
  default: () => <h1>Data Logger Inventory</h1>,
}));

vi.mock("./features/datalogger/pages/DataLoggerWizardPage", () => ({
  default: () => <h1>Data Logger Wizard</h1>,
}));

vi.mock("./features/datalogger/pages/DataLoggerDetailPage", () => ({
  default: () => <h1>Data Logger History</h1>,
}));

vi.mock("./features/datalogger/pages/DataLoggerQueryPage", () => ({
  default: () => <h1>Data Logger Query</h1>,
}));

vi.mock("./features/report/pages/ReportListPage", () => ({
  default: () => <h1>Report Inventory</h1>,
}));

vi.mock("./features/report/pages/ReportBuilderPage", () => ({
  default: () => <h1>Report Builder</h1>,
}));

vi.mock("./features/report/pages/ReportDetailPage", () => ({
  default: () => <h1>Report Detail</h1>,
}));

const mockedGetHealth = vi.mocked(getHealth);
const mockedListVGateways = vi.mocked(listVGateways);
const mockedListDeviceInventory = vi.mocked(listDeviceInventory);
const mockedListTags = vi.mocked(listTags);
const mockedListDataLoggers = vi.mocked(listDataLoggers);
const mockedCheckSetupStatus = vi.mocked(checkSetupStatus);
const mockedGetMe = vi.mocked(getMe);
const mockedLogin = vi.mocked(login);
const mockedRefreshAccessToken = vi.mocked(refreshAccessToken);
const mockedSetup = vi.mocked(setup);

const user: AuthUser = {
  id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
  username: "admin",
  created_at: "2026-08-21T08:00:00Z",
  last_login: null,
};

function renderAuthenticatedApp(path = "/dashboard") {
  window.history.replaceState({}, "", path);
  useAuthStore.getState().setSession(user, "test-access-token");

  return render(<App />);
}

describe("App", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    useAuthStore.getState().clearSession();
    useAuthStore.setState({ setupRequired: null });
    mockedCheckSetupStatus.mockReset();
    mockedCheckSetupStatus.mockResolvedValue({ setup_required: false });
    mockedGetMe.mockReset();
    mockedGetHealth.mockReset();
    mockedListVGateways.mockReset();
    mockedListVGateways.mockResolvedValue({ data: [], pagination: { page: 1, per_page: 100, total: 0, total_pages: 0 } });
    mockedListDeviceInventory.mockReset();
    mockedListDeviceInventory.mockResolvedValue({ data: [], pagination: { page: 1, per_page: 1, total: 0, total_pages: 0 } });
    mockedListTags.mockReset();
    mockedListTags.mockResolvedValue({ data: [], pagination: { page: 1, per_page: 8, total: 0, total_pages: 0 } });
    mockedListDataLoggers.mockReset();
    mockedListDataLoggers.mockResolvedValue({ data: [], pagination: { page: 1, per_page: 100, total: 0, total_pages: 0 } });
    mockedLogin.mockReset();
    mockedRefreshAccessToken.mockReset();
    mockedSetup.mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  it("redirects to login when the refresh cookie cannot restore a session", async () => {
    mockedRefreshAccessToken.mockRejectedValue(new Error("session expired"));
    useAuthStore.setState({
      isInitialized: false,
      isLoading: true,
    });

    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "Welcome back" }),
    ).toBeInTheDocument();
    expect(window.location.pathname).toBe("/login");
    expect(mockedCheckSetupStatus).toHaveBeenCalledOnce();
    expect(mockedRefreshAccessToken).toHaveBeenCalledOnce();
    expect(mockedGetHealth).not.toHaveBeenCalled();
  });

  it("restores a cookie-backed session before rendering the dashboard", async () => {
    mockedRefreshAccessToken.mockResolvedValue({
      access_token: "restored-access-token",
      expires_in: 900,
    });
    mockedGetMe.mockResolvedValue(user);
    mockedGetHealth.mockReturnValue(new Promise(() => {}));
    useAuthStore.setState({
      isInitialized: false,
      isLoading: true,
    });

    render(<App />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Checking your session…",
    );
    expect(await screen.findByText("Checking backend connection…")).toBeInTheDocument();
    expect(window.location.pathname).toBe("/dashboard");
    expect(mockedRefreshAccessToken).toHaveBeenCalledOnce();
    expect(mockedGetMe).toHaveBeenCalledOnce();
  });

  it("routes first-time visitors to setup without attempting refresh", async () => {
    mockedCheckSetupStatus.mockResolvedValue({ setup_required: true });
    useAuthStore.setState({
      isInitialized: false,
      isLoading: true,
    });

    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "Set up your account" }),
    ).toBeInTheDocument();
    expect(window.location.pathname).toBe("/setup");
    expect(mockedRefreshAccessToken).not.toHaveBeenCalled();
  });

  it("keeps the setup route available while setup is required", () => {
    window.history.replaceState({}, "", "/setup");
    useAuthStore.setState({ setupRequired: true });

    render(<App />);

    expect(
      screen.getByRole("heading", { name: "Set up your account" }),
    ).toBeInTheDocument();
    expect(mockedGetHealth).not.toHaveBeenCalled();
  });

  it("completes the mocked setup and login flow into the dashboard", async () => {
    mockedCheckSetupStatus.mockResolvedValue({ setup_required: true });
    mockedSetup.mockResolvedValue({
      message: "Setup completed successfully",
      user: {
        id: user.id,
        username: user.username,
        created_at: user.created_at,
      },
    });
    mockedLogin.mockResolvedValue({
      access_token: "login-access-token",
      token_type: "Bearer",
      expires_in: 900,
    });
    mockedGetMe.mockResolvedValue(user);
    mockedGetHealth.mockResolvedValue({
      status: "ok",
      version: "0.1.0",
    });
    useAuthStore.setState({
      isInitialized: false,
      isLoading: true,
    });

    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "Set up your account" }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "SecureP@ss123" },
    });
    fireEvent.change(screen.getByLabelText("Confirm password"), {
      target: { value: "SecureP@ss123" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(
      await screen.findByRole("heading", { name: "Welcome back" }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "SecureP@ss123" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Backend connected")).toBeInTheDocument();
    expect(window.location.pathname).toBe("/dashboard");
    expect(mockedSetup).toHaveBeenCalledOnce();
    expect(mockedLogin).toHaveBeenCalledOnce();
    expect(mockedGetMe).toHaveBeenCalledOnce();
  });

  it("shows a loading state while an authenticated user checks the backend", () => {
    mockedGetHealth.mockReturnValue(new Promise(() => {}));

    renderAuthenticatedApp();

    expect(screen.getByText("Checking backend connection…")).toBeInTheDocument();
  });

  it("shows backend status and version when the health check succeeds", async () => {
    mockedGetHealth.mockResolvedValue({
      status: "ok",
      version: "0.1.0",
    });

    renderAuthenticatedApp();

    expect(await screen.findByText("Backend connected")).toBeInTheDocument();
    expect(screen.getByText("ok · version 0.1.0")).toBeInTheDocument();
  });

  it("shows an error state when the health check fails", async () => {
    mockedGetHealth.mockRejectedValue(new Error("connection failed"));

    renderAuthenticatedApp();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Backend unavailable",
    );
  });

  it("protects and renders the Tag wizard route", () => {
    renderAuthenticatedApp("/tags/new");

    expect(screen.getByRole("heading", { name: "Add Tag Wizard" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/tags/new");
  });

  it("protects and renders the Tag monitoring detail route", () => {
    renderAuthenticatedApp("/tags/550e8400-e29b-41d4-a716-446655440000");

    expect(screen.getByRole("heading", { name: "Real-time Tag Monitor" })).toBeInTheDocument();
  });

  it("protects and renders the global Devices route", () => {
    renderAuthenticatedApp("/devices");

    expect(screen.getByRole("heading", { name: "Global Device Inventory" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/devices");
  });

  it("protects and renders the Data Logger inventory route", () => {
    renderAuthenticatedApp("/data-loggers");

    expect(screen.getByRole("heading", { name: "Data Logger Inventory" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/data-loggers");
  });

  it.each(["/data-loggers/new", "/data-loggers/logger-id/edit"])(
    "protects and renders the Data Logger wizard route at %s",
    (path) => {
      renderAuthenticatedApp(path);

      expect(screen.getByRole("heading", { name: "Data Logger Wizard" })).toBeInTheDocument();
      expect(window.location.pathname).toBe(path);
    },
  );

  it("protects and renders the Data Logger history route", () => {
    renderAuthenticatedApp("/data-loggers/logger-id");

    expect(screen.getByRole("heading", { name: "Data Logger History" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/data-loggers/logger-id");
  });

  it("protects and renders the Data Logger query route", () => {
    renderAuthenticatedApp("/data-loggers/logger-id/query");

    expect(screen.getByRole("heading", { name: "Data Logger Query" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/data-loggers/logger-id/query");
  });

  it("protects and renders the Report inventory route", () => {
    renderAuthenticatedApp("/reports");
    expect(screen.getByRole("heading", { name: "Report Inventory" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/reports");
  });

  it.each(["/reports/new", "/reports/report-id/edit"])(
    "protects and renders the Report builder route at %s",
    (path) => {
      renderAuthenticatedApp(path);
      expect(screen.getByRole("heading", { name: "Report Builder" })).toBeInTheDocument();
      expect(window.location.pathname).toBe(path);
    },
  );

  it("protects and renders the Report detail route", () => {
    renderAuthenticatedApp("/reports/report-id");
    expect(screen.getByRole("heading", { name: "Report Detail" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/reports/report-id");
  });
});
