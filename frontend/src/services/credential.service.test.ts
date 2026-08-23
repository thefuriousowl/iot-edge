import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CredentialProfile } from "../types/credential";
import api from "./api";
import { createCredential, deleteCredential, deleteCredentialSecret, getCredential, listCredentials, putCredentialSecret, updateCredential } from "./credential.service";

vi.mock("./api", () => ({ default: { delete: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() } }));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);
const profile: CredentialProfile = {
  id: "credential/1", type: "mqtt", name: "Plant broker", secret_revision: 0, usage_count: 0, secrets: [],
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};
const responseWith = <T,>(data: T) => ({ data }) as AxiosResponse<T>;

describe("Credential service", () => {
  beforeEach(() => {
    mockedDelete.mockReset(); mockedGet.mockReset(); mockedPost.mockReset(); mockedPut.mockReset();
  });

  it("covers profile CRUD and write-only slot endpoints", async () => {
    const controller = new AbortController();
    mockedGet.mockResolvedValueOnce(responseWith({ data: [profile] })).mockResolvedValueOnce(responseWith(profile));
    mockedPost.mockResolvedValueOnce(responseWith(profile));
    mockedPut.mockResolvedValueOnce(responseWith(profile)).mockResolvedValueOnce(responseWith(undefined));
    mockedDelete.mockResolvedValue(responseWith(undefined));

    await expect(listCredentials({ type: "mqtt", search: "plant" }, controller.signal)).resolves.toEqual([profile]);
    await expect(getCredential(profile.id, controller.signal)).resolves.toEqual(profile);
    await expect(createCredential({ type: "mqtt", name: "Plant broker", description: null })).resolves.toEqual(profile);
    await expect(updateCredential(profile.id, { name: "Updated" })).resolves.toEqual(profile);
    await expect(putCredentialSecret(profile.id, "mqtt.password", { value_base64: "dGVzdA==" })).resolves.toBeUndefined();
    await expect(deleteCredentialSecret(profile.id, "mqtt.password")).resolves.toBeUndefined();
    await expect(deleteCredential(profile.id)).resolves.toBeUndefined();

    expect(mockedGet).toHaveBeenNthCalledWith(1, "/credentials", { params: { type: "mqtt", search: "plant" }, signal: controller.signal });
    expect(mockedPut).toHaveBeenLastCalledWith("/credentials/credential%2F1/secrets/mqtt.password", { value_base64: "dGVzdA==" });
    expect(mockedDelete).toHaveBeenCalledWith("/credentials/credential%2F1/secrets/mqtt.password");
  });
});
