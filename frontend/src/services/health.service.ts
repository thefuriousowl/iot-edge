import api from "./api";

export interface HealthResponse {
  status: string;
  version: string;
}

export interface InternetStatusResponse {
  status: "online" | "offline";
  checked_at: string;
  latency_ms: number | null;
}

export async function getHealth(
  signal?: AbortSignal,
): Promise<HealthResponse> {
  const response = await api.get<HealthResponse>("/health", { signal });

  return response.data;
}

export async function getInternetStatus(
  signal?: AbortSignal,
): Promise<InternetStatusResponse> {
  const response = await api.get<InternetStatusResponse>(
    "/system/internet-status",
    { signal },
  );

  return response.data;
}
