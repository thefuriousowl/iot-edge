import type { CredentialProfile, CredentialType, PutCredentialSecretRequest } from "../types/credential";
import type { CredentialSecretSlot } from "../types/publisher";
import api from "./api";

export async function listCredentials(params?: { type?: CredentialType; search?: string }, signal?: AbortSignal): Promise<CredentialProfile[]> {
  const response = await api.get<{ data: CredentialProfile[] }>("/credentials", { params, signal });
  return response.data.data;
}

export async function getCredential(id: string, signal?: AbortSignal): Promise<CredentialProfile> {
  const response = await api.get<CredentialProfile>(`/credentials/${encodeURIComponent(id)}`, { signal });
  return response.data;
}

export async function createCredential(data: { type: CredentialType; name: string; description: string | null }): Promise<CredentialProfile> {
  const response = await api.post<CredentialProfile>("/credentials", data);
  return response.data;
}

export async function updateCredential(id: string, data: { name?: string; description?: string | null }): Promise<CredentialProfile> {
  const response = await api.put<CredentialProfile>(`/credentials/${encodeURIComponent(id)}`, data);
  return response.data;
}

export async function deleteCredential(id: string): Promise<void> {
  await api.delete(`/credentials/${encodeURIComponent(id)}`);
}

export async function putCredentialSecret(id: string, slot: CredentialSecretSlot, data: PutCredentialSecretRequest): Promise<void> {
  await api.put(`/credentials/${encodeURIComponent(id)}/secrets/${encodeURIComponent(slot)}`, data);
}

export async function deleteCredentialSecret(id: string, slot: CredentialSecretSlot): Promise<void> {
  await api.delete(`/credentials/${encodeURIComponent(id)}/secrets/${encodeURIComponent(slot)}`);
}
