// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { listTags } from "../../../services/tag.service";
import type { Tag, TagListResponse } from "../../../types/tag";
import TagListPage from "./TagListPage";

vi.mock("../../../services/tag.service", () => ({
  listTags: vi.fn(),
}));

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

const mockedListTags = vi.mocked(listTags);

const tags: Tag[] = [
  {
    id: "11111111-1111-4111-8111-111111111111",
    datasource_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    name: "Line voltage",
    type: "reading",
    data_type: "uint16",
    description: "Main incomer",
    enabled: true,
    config: {
      decoder: {
        type: "binary_numeric",
        config: {
          byte_offset: 0,
          byte_order: "little_endian",
          bit_offset: 0,
        },
      },
    },
    created_at: "2026-08-22T01:00:00Z",
    updated_at: "2026-08-22T02:00:00Z",
  },
  {
    id: "22222222-2222-4222-8222-222222222222",
    datasource_id: null,
    name: "Nominal voltage",
    type: "constant",
    data_type: "float64",
    description: null,
    enabled: false,
    config: { value: 230.5 },
    created_at: "2026-08-22T01:00:00Z",
    updated_at: "2026-08-22T02:00:00Z",
  },
  {
    id: "33333333-3333-4333-8333-333333333333",
    datasource_id: null,
    name: "Voltage delta",
    type: "calculated",
    data_type: "float64",
    description: "Deviation from nominal",
    enabled: true,
    config: { expression: "${11111111-1111-4111-8111-111111111111} - 230.5", trigger: { tag_id: "11111111-1111-4111-8111-111111111111", mode: "on_sample" } },
    created_at: "2026-08-22T01:00:00Z",
    updated_at: "2026-08-22T02:00:00Z",
  },
];

function response(
  data: Tag[] = tags,
  overrides?: Partial<TagListResponse>,
): TagListResponse {
  return {
    data,
    pagination: {
      page: 1,
      per_page: 20,
      total: data.length,
      total_pages: data.length > 0 ? 1 : 0,
    },
    ...overrides,
  };
}

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location-search">{location.search}</div>;
}

function renderPage(path = "/tags") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TagListPage />
      <LocationProbe />
    </MemoryRouter>,
  );
}

function currentSearchParams(): URLSearchParams {
  return new URLSearchParams(screen.getByTestId("location-search").textContent ?? "");
}

describe("TagListPage", () => {
  beforeEach(() => {
    mockedListTags.mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  it("shows loading, summary metrics, and every tag definition", async () => {
    let resolveList: ((value: TagListResponse) => void) | undefined;
    mockedListTags.mockReturnValue(
      new Promise((resolve) => {
        resolveList = resolve;
      }),
    );

    renderPage();

    expect(screen.getByRole("status")).toHaveTextContent("Loading tags");
    resolveList?.(response());

    expect(await screen.findByText("Line voltage")).toBeInTheDocument();
    expect(screen.getByText("Nominal voltage")).toBeInTheDocument();
    expect(screen.getByText("Voltage delta")).toBeInTheDocument();
    expect(screen.getByText("Little Endian · offset 0")).toBeInTheDocument();
    expect(screen.getByText("230.5")).toBeInTheDocument();
    expect(
      screen.getByText("${11111111-1111-4111-8111-111111111111} - 230.5"),
    ).toBeInTheDocument();
    expect(screen.getByText("aaaaaaaa…")).toHaveAttribute(
      "title",
      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    );
    expect(screen.getByRole("link", { name: "Tags" })).toHaveClass("active");
    expect(screen.getByRole("link", { name: "Add Tag" })).toHaveAttribute(
      "href",
      "/tags/new",
    );
    expect(screen.getByRole("link", { name: /Line voltage/ })).toHaveAttribute("href", "/tags/11111111-1111-4111-8111-111111111111");

    const summary = screen.getByRole("region", { name: "Tag summary" });
    expect(within(summary).getByText("3")).toBeInTheDocument();
    expect(within(summary).getAllByText("1")).toHaveLength(2);
    expect(mockedListTags).toHaveBeenCalledWith(
      {
        type: undefined,
        data_type: undefined,
        enabled: undefined,
        search: undefined,
        page: 1,
        per_page: 20,
      },
      expect.any(AbortSignal),
    );
  });

  it("applies all server-side filters and keeps them in the URL", async () => {
    mockedListTags.mockResolvedValue(response());
    renderPage();
    await screen.findByText("Line voltage");

    fireEvent.change(screen.getByRole("combobox", { name: "Tag type" }), {
      target: { value: "reading" },
    });
    await waitFor(() => expect(mockedListTags).toHaveBeenCalledTimes(2));

    fireEvent.change(screen.getByRole("combobox", { name: "Data type" }), {
      target: { value: "uint16" },
    });
    await waitFor(() => expect(mockedListTags).toHaveBeenCalledTimes(3));

    fireEvent.change(screen.getByRole("combobox", { name: "Enabled state" }), {
      target: { value: "enabled" },
    });
    await waitFor(() => expect(mockedListTags).toHaveBeenCalledTimes(4));

    fireEvent.change(screen.getByRole("searchbox", { name: "Search tags" }), {
      target: { value: " voltage " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    await waitFor(() => {
      expect(mockedListTags).toHaveBeenLastCalledWith(
        {
          type: "reading",
          data_type: "uint16",
          enabled: true,
          search: "voltage",
          page: 1,
          per_page: 20,
        },
        expect.any(AbortSignal),
      );
    });
    const params = currentSearchParams();
    expect(params.get("type")).toBe("reading");
    expect(params.get("data_type")).toBe("uint16");
    expect(params.get("enabled")).toBe("true");
    expect(params.get("search")).toBe("voltage");
    expect(screen.getByText("4 active filters")).toBeInTheDocument();
  });

  it("hydrates filters from the URL, paginates, and resets page on filter changes", async () => {
    mockedListTags.mockResolvedValue(
      response(tags, {
        pagination: { page: 2, per_page: 20, total: 43, total_pages: 3 },
      }),
    );
    renderPage(
      "/tags?type=calculated&data_type=float64&enabled=false&search=delta&page=2",
    );

    await screen.findByText("Voltage delta");
    expect(mockedListTags).toHaveBeenCalledWith(
      {
        type: "calculated",
        data_type: "float64",
        enabled: false,
        search: "delta",
        page: 2,
        per_page: 20,
      },
      expect.any(AbortSignal),
    );
    expect(screen.getByRole("combobox", { name: "Tag type" })).toHaveValue(
      "calculated",
    );
    expect(screen.getByRole("searchbox", { name: "Search tags" })).toHaveValue(
      "delta",
    );

    fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
    await waitFor(() => {
      expect(mockedListTags).toHaveBeenLastCalledWith(
        expect.objectContaining({ page: 1 }),
        expect.any(AbortSignal),
      );
    });
    expect(currentSearchParams().get("page")).toBe("1");

    fireEvent.change(screen.getByRole("combobox", { name: "Tag type" }), {
      target: { value: "all" },
    });
    await waitFor(() => {
      expect(currentSearchParams().has("page")).toBe(false);
      expect(currentSearchParams().has("type")).toBe(false);
    });
  });

  it("shows API failures and retries the same query", async () => {
    mockedListTags
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockResolvedValueOnce(response());
    renderPage("/tags?type=reading");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Unable to load tags",
    );
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(await screen.findByText("Line voltage")).toBeInTheDocument();
    expect(mockedListTags).toHaveBeenCalledTimes(2);
    expect(mockedListTags).toHaveBeenLastCalledWith(
      expect.objectContaining({ type: "reading" }),
      expect.any(AbortSignal),
    );
  });

  it("distinguishes an empty namespace from filtered empty results", async () => {
    mockedListTags.mockResolvedValueOnce(response([]));
    const first = renderPage();
    expect(await screen.findByText("No tags yet")).toBeInTheDocument();
    first.unmount();

    mockedListTags
      .mockResolvedValueOnce(response([]))
      .mockResolvedValueOnce(response());
    renderPage("/tags?search=missing");
    expect(await screen.findByText("No matching tags")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));

    expect(await screen.findByText("Line voltage")).toBeInTheDocument();
    expect(currentSearchParams().toString()).toBe("");
  });

  it("aborts the in-flight request when leaving the page", async () => {
    mockedListTags.mockReturnValue(new Promise(() => {}));
    const rendered = renderPage();
    await waitFor(() => expect(mockedListTags).toHaveBeenCalledOnce());
    const signal = mockedListTags.mock.calls[0][1];

    rendered.unmount();

    expect(signal?.aborted).toBe(true);
  });
});
