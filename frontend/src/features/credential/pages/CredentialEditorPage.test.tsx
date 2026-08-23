// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { deleteCredentialSecret, getCredential, putCredentialSecret } from "../../../services/credential.service";
import type { CredentialProfile } from "../../../types/credential";
import CredentialEditorPage from "./CredentialEditorPage";

vi.mock("../../../services/credential.service", () => ({
  createCredential: vi.fn(), deleteCredentialSecret: vi.fn(), getCredential: vi.fn(),
  putCredentialSecret: vi.fn(), updateCredential: vi.fn(),
}));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Online</span> }));

const mockedDelete = vi.mocked(deleteCredentialSecret);
const mockedGet = vi.mocked(getCredential);
const mockedPut = vi.mocked(putCredentialSecret);
const baseProfile: CredentialProfile = {
  id: "credential-1", type: "mqtt", name: "Plant broker", secret_revision: 0, usage_count: 1, secrets: [],
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

describe("CredentialEditorPage", () => {
  beforeEach(() => {
    mockedGet.mockReset().mockResolvedValue(baseProfile);
    mockedPut.mockReset().mockResolvedValue(undefined);
    mockedDelete.mockReset().mockResolvedValue(undefined);
  });
  afterEach(() => cleanup());

  function renderPage() {
    render(<MemoryRouter initialEntries={["/credentials/credential-1"]}><Routes><Route path="/credentials/:id" element={<CredentialEditorPage />} /></Routes></MemoryRouter>);
  }

  it("stores an optional password once, clears the field, and never renders it back", async () => {
    const rotated: CredentialProfile = {
      ...baseProfile, secret_revision: 1,
      secrets: [{ slot: "mqtt.password", kind: "opaque", revision: 1, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T00:00:00Z" }],
    };
    mockedGet.mockResolvedValueOnce(baseProfile).mockResolvedValueOnce(rotated);
    renderPage();
    const password = await screen.findByLabelText(/^Password/);
    fireEvent.change(password, { target: { value: "temporary-test-password" } });
    fireEvent.click(within(password.closest("article")!).getByRole("button", { name: "Store" }));

    await waitFor(() => expect(mockedPut).toHaveBeenCalledWith("credential-1", "mqtt.password", { value_base64: "dGVtcG9yYXJ5LXRlc3QtcGFzc3dvcmQ=" }));
    expect(password).toHaveValue("");
    expect(screen.queryByText("temporary-test-password")).not.toBeInTheDocument();
    expect(await screen.findByRole("status")).toHaveTextContent("cannot be read back");
  });

  it("shows all optional slots without requiring username or password", async () => {
    renderPage();
    expect(await screen.findByLabelText(/^Username/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Password/)).toBeInTheDocument();
    expect(screen.getByText("Custom CA certificate")).toBeInTheDocument();
    expect(screen.getByText("mTLS client identity")).toBeInTheDocument();
    expect(screen.getByText(/Store only what the destination requires/)).toBeInTheDocument();
  });

  it("stores HTTP API keys in the HTTP profile namespace without rendering them back", async () => {
    const httpProfile: CredentialProfile = { ...baseProfile, type: "http", name: "Plant HTTP API" };
    const rotated: CredentialProfile = {
      ...httpProfile, secret_revision: 1,
      secrets: [{ slot: "http.api_key", kind: "opaque", revision: 1, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T00:00:00Z" }],
    };
    mockedGet.mockReset().mockResolvedValueOnce(httpProfile).mockResolvedValueOnce(rotated);
    renderPage();

    const apiKey = await screen.findByLabelText(/^API key/);
    fireEvent.change(apiKey, { target: { value: "temporary-http-key" } });
    fireEvent.click(within(apiKey.closest("article")!).getByRole("button", { name: "Store" }));

    await waitFor(() => expect(mockedPut).toHaveBeenCalledWith("credential-1", "http.api_key", { value_base64: "dGVtcG9yYXJ5LWh0dHAta2V5" }));
    expect(apiKey).toHaveValue("");
    expect(screen.queryByText("temporary-http-key")).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^Bearer token/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^OAuth client secret/)).toBeInTheDocument();
  });
});
