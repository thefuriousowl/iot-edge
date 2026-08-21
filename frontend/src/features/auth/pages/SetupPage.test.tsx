// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { APP_VERSION } from "../../../config/app";
import { setup } from "../../../services/auth.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { SetupResponse } from "../../../types/auth";
import SetupPage from "./SetupPage";

vi.mock("../../../services/auth.service", () => ({
  setup: vi.fn(),
}));

const mockedSetup = vi.mocked(setup);

const setupResponse: SetupResponse = {
  message: "Initial setup completed successfully",
  user: {
    id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
    username: "admin",
    created_at: "2026-08-21T08:00:00Z",
  },
};

function renderSetupPage() {
  return render(
    <MemoryRouter initialEntries={["/setup"]}>
      <Routes>
        <Route path="/setup" element={<SetupPage />} />
        <Route path="/login" element={<p>Login reached</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

function submitValidSetupForm() {
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
}

describe("SetupPage", () => {
  beforeEach(() => {
    mockedSetup.mockReset();
    useAuthStore.getState().clearSession();
    useAuthStore.setState({ setupRequired: true });
  });

  afterEach(() => {
    cleanup();
  });

  it("renders the administrator setup controls", () => {
    renderSetupPage();

    expect(
      screen.getByRole("heading", { name: "Set up your account" }),
    ).toBeInTheDocument();
    expect(screen.getByText(`v${APP_VERSION}`)).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toHaveAttribute(
      "autocomplete",
      "username",
    );
    expect(screen.getByLabelText("Password")).toHaveAttribute(
      "autocomplete",
      "new-password",
    );
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute(
      "autocomplete",
      "new-password",
    );
    expect(
      screen.getByRole("button", { name: "Create account" }),
    ).toBeEnabled();
  });

  it("shows required field errors before accepting the form", async () => {
    renderSetupPage();

    fireEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(await screen.findByText("Username is required")).toBeInTheDocument();
    expect(screen.getByText("Password is required")).toBeInTheDocument();
    expect(screen.getByText("Confirm password is required")).toBeInTheDocument();
    expect(mockedSetup).not.toHaveBeenCalled();
  });

  it("validates password complexity and confirmation", async () => {
    renderSetupPage();

    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "adminpassword" },
    });
    fireEvent.change(screen.getByLabelText("Confirm password"), {
      target: { value: "different" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(
      await screen.findByText("Password must contain at least 3 character types"),
    ).toBeInTheDocument();
    expect(screen.getByText("Passwords do not match")).toBeInTheDocument();
  });

  it("rejects a password containing the username", async () => {
    renderSetupPage();

    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "Admin@123Secure" },
    });
    fireEvent.change(screen.getByLabelText("Confirm password"), {
      target: { value: "Admin@123Secure" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(
      await screen.findByText("Password must not contain your username"),
    ).toBeInTheDocument();
  });

  it("updates requirements and toggles both password fields", () => {
    renderSetupPage();
    const password = screen.getByLabelText("Password");
    const confirmPassword = screen.getByLabelText("Confirm password");

    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(password, {
      target: { value: "SecureP@ss123" },
    });

    expect(screen.getByText("8–128 characters").closest("li")).toHaveClass(
      "is-valid",
    );
    expect(
      screen
        .getByText(
          "At least 3 of: uppercase, lowercase, number, special character",
        )
        .closest("li"),
    ).toHaveClass("is-valid");
    expect(
      screen.getByText("Does not contain your username").closest("li"),
    ).toHaveClass("is-valid");

    fireEvent.click(screen.getByRole("button", { name: "Show password" }));
    fireEvent.click(
      screen.getByRole("button", { name: "Show confirm password" }),
    );

    expect(password).toHaveAttribute("type", "text");
    expect(confirmPassword).toHaveAttribute("type", "text");
  });

  it("submits valid values and redirects to login", async () => {
    mockedSetup.mockResolvedValue(setupResponse);
    renderSetupPage();

    submitValidSetupForm();

    expect(await screen.findByText("Login reached")).toBeInTheDocument();
    expect(mockedSetup).toHaveBeenCalledWith({
      username: "admin",
      password: "SecureP@ss123",
      confirm_password: "SecureP@ss123",
    });
    expect(mockedSetup).toHaveBeenCalledOnce();
    expect(useAuthStore.getState().setupRequired).toBe(false);
  });

  it("shows the backend error and stays on setup", async () => {
    mockedSetup.mockRejectedValue({
      isAxiosError: true,
      response: {
        data: {
          error: {
            code: "SETUP_ALREADY_COMPLETED",
            message: "Initial setup has already been completed",
          },
        },
      },
    });
    renderSetupPage();

    submitValidSetupForm();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Initial setup has already been completed",
    );
    expect(screen.queryByText("Login reached")).not.toBeInTheDocument();
  });

  it("disables submission while the setup request is pending", async () => {
    let resolveSetup!: (response: SetupResponse) => void;
    mockedSetup.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveSetup = resolve;
        }),
    );
    renderSetupPage();

    submitValidSetupForm();

    const submittingButton = await screen.findByRole("button", {
      name: "Creating account…",
    });
    expect(submittingButton).toBeDisabled();

    fireEvent.click(submittingButton);
    expect(mockedSetup).toHaveBeenCalledOnce();

    await act(async () => {
      resolveSetup(setupResponse);
    });
    await waitFor(() => {
      expect(screen.getByText("Login reached")).toBeInTheDocument();
    });
  });
});
