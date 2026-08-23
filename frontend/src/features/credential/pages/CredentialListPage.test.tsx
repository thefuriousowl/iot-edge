// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import { listCredentials } from "../../../services/credential.service";
import type { CredentialProfile } from "../../../types/credential";
import CredentialListPage from "./CredentialListPage";

vi.mock("../../../services/credential.service", () => ({ deleteCredential: vi.fn(), listCredentials: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Online</span> }));

const mockedList = vi.mocked(listCredentials);
const profiles: CredentialProfile[] = [
  {
    id: "http-1", type: "http", name: "Plant API", secret_revision: 2, usage_count: 1,
    secrets: [{ slot: "http.api_key", kind: "opaque", revision: 2, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T01:00:00Z" }],
    created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T01:00:00Z",
  },
  {
    id: "mqtt-1", type: "mqtt", name: "Plant broker", secret_revision: 1, usage_count: 0,
    secrets: [], created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
  },
];

describe("CredentialListPage", () => {
  beforeEach(() => mockedList.mockReset().mockResolvedValue(profiles));
  afterEach(() => cleanup());

  it("lists both profile namespaces and their actual slot capacities", async () => {
    render(<MemoryRouter><CredentialListPage /></MemoryRouter>);

    expect(await screen.findByText("Plant API")).toBeInTheDocument();
    expect(screen.getByText("Plant broker")).toBeInTheDocument();
    expect(screen.getByText("1 of 7 slots")).toBeInTheDocument();
    expect(screen.getByText("0 of 4 slots")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New HTTP Profile" })).toHaveAttribute("href", "/credentials/new?type=http");
    expect(screen.getByRole("link", { name: "New MQTT Profile" })).toHaveAttribute("href", "/credentials/new?type=mqtt");
  });

  it("passes an explicit HTTP transport filter to Core", async () => {
    render(<MemoryRouter initialEntries={["/credentials?type=http"]}><CredentialListPage /></MemoryRouter>);

    await screen.findByText("Plant API");
    await waitFor(() => expect(mockedList).toHaveBeenCalledWith({ type: "http", search: undefined }, expect.any(AbortSignal)));
  });
});
