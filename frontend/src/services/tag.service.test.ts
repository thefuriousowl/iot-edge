import type { AxiosResponse } from "axios";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  CreateTagRequest,
  Tag,
  TagListResponse,
  TagPreviewResult,
  TagRuntimeValue,
  TagValuesResponse,
  UpdateTagRequest,
  ValidateTagExpressionResponse,
} from "../types/tag";
import api from "./api";
import {
  createTag,
  deleteTag,
  getTag,
  getTagValues,
  listTags,
  monitorTagValues,
  previewSavedTag,
  previewTag,
  updateTag,
  validateTagExpression,
} from "./tag.service";

vi.mock("./api", () => ({
  default: {
    delete: vi.fn(),
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  getAccessToken: vi.fn(() => "access-token"),
}));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);

const tag: Tag = {
  id: "550e8400-e29b-41d4-a716-446655440000",
  datasource_id: null,
  name: "Nominal voltage",
  type: "constant",
  data_type: "float64",
  description: null,
  enabled: true,
  config: { value: 230.5 },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

function responseWith<T>(data: T): AxiosResponse<T> {
  return { data } as AxiosResponse<T>;
}

describe("Tag service", () => {
  beforeEach(() => {
    mockedDelete.mockReset();
    mockedGet.mockReset();
    mockedPost.mockReset();
    mockedPut.mockReset();
  });

  afterEach(() => vi.unstubAllGlobals());

  it("lists tags with every filter, pagination, and cancellation", async () => {
    const controller = new AbortController();
    const params = {
      type: "reading" as const,
      data_type: "uint16" as const,
      enabled: true,
      datasource_id: "source-1",
      search: "voltage",
      page: 2,
      per_page: 20,
    };
    const expected: TagListResponse = {
      data: [tag],
      pagination: { page: 2, per_page: 20, total: 21, total_pages: 2 },
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(listTags(params, controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/tags", {
      params,
      signal: controller.signal,
    });
  });

  it("creates and gets a tag without transforming its typed config", async () => {
    const request: CreateTagRequest = {
      name: tag.name,
      type: "constant",
      data_type: "float64",
      description: null,
      config: { value: 230.5 },
    };
    const controller = new AbortController();
    mockedPost.mockResolvedValue(responseWith(tag));
    mockedGet.mockResolvedValue(responseWith(tag));

    await expect(createTag(request)).resolves.toEqual(tag);
    expect(mockedPost).toHaveBeenCalledWith("/tags", request);
    await expect(getTag(tag.id, controller.signal)).resolves.toEqual(tag);
    expect(mockedGet).toHaveBeenCalledWith(`/tags/${tag.id}`, {
      signal: controller.signal,
    });
  });

  it("loads bounded runtime history and parses fragmented Tag SSE values", async () => {
    const value: TagRuntimeValue = { tag_id: tag.id, sequence: 7, observed_at: "2026-08-22T01:00:00Z", stored_at: "2026-08-22T01:00:00Z", quality: "good", data_type: "float64", value: 230.5 };
    const expected: TagValuesResponse = { latest: value, history: [value], latest_retention: "persistent", history_retention: "runtime_memory" };
    mockedGet.mockResolvedValue(responseWith(expected));
    const controller = new AbortController();
    await expect(getTagValues(tag.id, 10, controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith(`/tags/${tag.id}/values`, { params: { limit: 10 }, signal: controller.signal });

    const encoded = new TextEncoder().encode(`: connected\n\nevent: tag_value\ndata: ${JSON.stringify(value)}\n\n`);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(new ReadableStream({ start(stream) { stream.enqueue(encoded.slice(0, 15)); stream.enqueue(encoded.slice(15)); stream.close(); } }), { status: 200 })));
    const received: TagRuntimeValue[] = [];
    const onOpen = vi.fn();
    await monitorTagValues(tag.id, (next) => received.push(next), new AbortController().signal, onOpen);
    expect(onOpen).toHaveBeenCalledOnce();
    expect(received).toEqual([value]);
    expect(fetch).toHaveBeenCalledWith(`/api/tags/${tag.id}/stream`, expect.objectContaining({ headers: { Authorization: "Bearer access-token" }, credentials: "include" }));
  });

  it("updates nullable fields and deletes through an encoded resource path", async () => {
    const id = "tag with/slash";
    const request: UpdateTagRequest = {
      datasource_id: null,
      description: null,
      enabled: false,
      config: { value: 220 },
    };
    const expected: Tag = { ...tag, enabled: false, config: { value: 220 } };
    mockedPut.mockResolvedValue(responseWith(expected));
    mockedDelete.mockResolvedValue(responseWith(undefined));

    await expect(updateTag(id, request)).resolves.toEqual(expected);
    expect(mockedPut).toHaveBeenCalledWith("/tags/tag%20with%2Fslash", request);
    await expect(deleteTag(id)).resolves.toBeUndefined();
    expect(mockedDelete).toHaveBeenCalledWith("/tags/tag%20with%2Fslash");
  });

  it("previews unsaved and saved tags", async () => {
    const request = {
      type: "reading" as const,
      datasource_id: "source-1",
      data_type: "uint16" as const,
      config: {
        decoder: {
          type: "binary_numeric" as const,
          config: { byte_order: "little_endian" as const },
        },
      },
    };
    const unsaved: TagPreviewResult = {
      tag_id: "00000000-0000-0000-0000-000000000000",
      observed_at: "2026-08-22T00:00:00Z",
      quality: "good",
      data_type: "uint16",
      value: 42,
    };
    const saved = { ...unsaved, tag_id: tag.id };
    mockedPost
      .mockResolvedValueOnce(responseWith(unsaved))
      .mockResolvedValueOnce(responseWith(saved));

    await expect(previewTag(request)).resolves.toEqual(unsaved);
    expect(mockedPost).toHaveBeenNthCalledWith(1, "/tags/preview", request);
    await expect(previewSavedTag(tag.id)).resolves.toEqual(saved);
    expect(mockedPost).toHaveBeenNthCalledWith(
      2,
      `/tags/${tag.id}/preview`,
    );
  });

  it("validates a candidate expression with its stable dependencies", async () => {
    const request = {
      tag_id: tag.id,
      expression: `\${${tag.id}} + 1`,
    };
    const expected: ValidateTagExpressionResponse = {
      valid: true,
      dependencies: [tag.id],
    };
    mockedPost.mockResolvedValue(responseWith(expected));

    const controller = new AbortController();

    await expect(validateTagExpression(request, controller.signal)).resolves.toEqual(expected);
    expect(mockedPost).toHaveBeenCalledWith(
      "/tags/validate-expression",
      request,
      { signal: controller.signal },
    );
  });

  it("does not hide API errors from callers", async () => {
    const expectedError = new Error("Tag name already exists");
    mockedPost.mockRejectedValue(expectedError);

    await expect(
      createTag({
        name: tag.name,
        type: "constant",
        data_type: "float64",
        config: { value: 230.5 },
      }),
    ).rejects.toBe(expectedError);
  });
});
