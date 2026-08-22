import type {
  CreateTagRequest,
  PreviewTagRequest,
  Tag,
  TagListParams,
  TagListResponse,
  TagPreviewResult,
  TagRuntimeValue,
  TagValuesResponse,
  UpdateTagRequest,
  ValidateTagExpressionRequest,
  ValidateTagExpressionResponse,
} from "../types/tag";
import type { SSEStreamContext } from "../types/sse";
import api, { getAccessToken } from "./api";

function tagPath(id: string): string {
  return `/tags/${encodeURIComponent(id)}`;
}

export async function listTags(
  params?: TagListParams,
  signal?: AbortSignal,
): Promise<TagListResponse> {
  const response = await api.get<TagListResponse>("/tags", {
    params,
    signal,
  });

  return response.data;
}

export async function createTag(data: CreateTagRequest): Promise<Tag> {
  const response = await api.post<Tag>("/tags", data);

  return response.data;
}

export async function getTag(
  id: string,
  signal?: AbortSignal,
): Promise<Tag> {
  const response = await api.get<Tag>(tagPath(id), { signal });

  return response.data;
}

export async function getTagValues(
  id: string,
  limit = 10,
  signal?: AbortSignal,
): Promise<TagValuesResponse> {
  const response = await api.get<TagValuesResponse>(`${tagPath(id)}/values`, {
    params: { limit },
    signal,
  });
  return response.data;
}

interface PendingSSEEvent {
  data: string[];
  event: string;
}

function dispatchTagValueEvent(
  pending: PendingSSEEvent,
  onValue: (value: TagRuntimeValue) => void,
): void {
  if (pending.event !== "tag_value" || pending.data.length === 0) return;

  try {
    onValue(JSON.parse(pending.data.join("\n")) as TagRuntimeValue);
  } catch {
    return;
  }
}

async function consumeTagValueStream(
  response: Response,
  context: SSEStreamContext<TagRuntimeValue>,
): Promise<void> {
  const reader = response.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let pending: PendingSSEEvent = { data: [], event: "" };

  const processLine = (line: string) => {
    if (line === "") {
      dispatchTagValueEvent(pending, context.onMessage);
      pending = { data: [], event: "" };
      return;
    }
    if (line.startsWith(":")) return;

    const separator = line.indexOf(":");
    const field = separator === -1 ? line : line.slice(0, separator);
    let value = separator === -1 ? "" : line.slice(separator + 1);
    if (value.startsWith(" ")) value = value.slice(1);

    if (field === "event") pending.event = value;
    if (field === "data") pending.data.push(value);
    if (field === "retry" && /^\d+$/.test(value)) context.onRetry(Number(value));
  };

  while (true) {
    const { done, value } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });

    let newline = buffer.indexOf("\n");
    while (newline !== -1) {
      const line = buffer.slice(0, newline).replace(/\r$/, "");
      buffer = buffer.slice(newline + 1);
      processLine(line);
      newline = buffer.indexOf("\n");
    }

    if (done) {
      if (buffer) processLine(buffer.replace(/\r$/, ""));
      dispatchTagValueEvent(pending, context.onMessage);
      return;
    }
  }
}

export async function monitorAllTagValues(
  context: SSEStreamContext<TagRuntimeValue>,
): Promise<void> {
  const baseURL = import.meta.env.VITE_API_URL || "/api";
  const token = getAccessToken();
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (context.lastEventId !== null) headers["Last-Event-ID"] = String(context.lastEventId);

  const response = await fetch(`${baseURL}/sse/tags`, {
    headers,
    credentials: "include",
    signal: context.signal,
  });
  if (!response.ok || !response.body) {
    throw new Error(`Tag live stream failed with status ${response.status}`);
  }

  context.onOpen();
  await consumeTagValueStream(response, context);
}

export async function updateTag(
  id: string,
  data: UpdateTagRequest,
): Promise<Tag> {
  const response = await api.put<Tag>(tagPath(id), data);

  return response.data;
}

export async function deleteTag(id: string): Promise<void> {
  await api.delete(tagPath(id));
}

export async function previewTag(
  data: PreviewTagRequest,
): Promise<TagPreviewResult> {
  const response = await api.post<TagPreviewResult>("/tags/preview", data);

  return response.data;
}

export async function previewSavedTag(id: string): Promise<TagPreviewResult> {
  const response = await api.post<TagPreviewResult>(`${tagPath(id)}/preview`);

  return response.data;
}

export async function validateTagExpression(
  data: ValidateTagExpressionRequest,
  signal?: AbortSignal,
): Promise<ValidateTagExpressionResponse> {
  const response = await api.post<ValidateTagExpressionResponse>(
    "/tags/validate-expression",
    data,
    { signal },
  );

  return response.data;
}
