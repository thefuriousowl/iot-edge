import type {
  CreateTagRequest,
  PreviewTagRequest,
  Tag,
  TagListParams,
  TagListResponse,
  TagPreviewResult,
  UpdateTagRequest,
  ValidateTagExpressionRequest,
  ValidateTagExpressionResponse,
} from "../types/tag";
import api from "./api";

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
