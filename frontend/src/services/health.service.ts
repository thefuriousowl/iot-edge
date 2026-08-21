import api from "./api";

export interface HealthResponse {
  status: string;
  version: string;
}

export async function getHealth(
  signal?: AbortSignal,
): Promise<HealthResponse> {
  const response = await api.get<HealthResponse>("/health", { signal });

  return response.data;
}
