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

export async function monitorTagValues(
  id: string,
  onValue: (value: TagRuntimeValue) => void,
  signal: AbortSignal,
  onOpen?: () => void,
): Promise<void> {
  const baseURL = import.meta.env.VITE_API_URL || "/api";
  const token = getAccessToken();
  const response = await fetch(`${baseURL}${tagPath(id)}/stream`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    credentials: "include",
    signal,
  });
  if (!response.ok || !response.body) {
    throw new Error(`Tag value stream failed with status ${response.status}`);
  }
  onOpen?.();

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) return;
    buffer += decoder.decode(value, { stream: true });
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";
    for (const event of events) {
      const dataLine = event.split("\n").find((line) => line.startsWith("data: "));
      if (dataLine) onValue(JSON.parse(dataLine.slice(6)) as TagRuntimeValue);
    }
  }
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
