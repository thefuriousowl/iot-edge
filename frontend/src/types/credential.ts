import type { CredentialSecretSlot } from "./publisher";

export type CredentialType = "mqtt";
export type CredentialSecretKind = "opaque" | "ca_certificate" | "client_identity";

export interface CredentialSecretMetadata {
  slot: CredentialSecretSlot;
  kind: CredentialSecretKind;
  revision: number;
  created_at: string;
  rotated_at: string;
}

export interface CredentialProfile {
  id: string;
  type: CredentialType;
  name: string;
  description?: string;
  secret_revision: number;
  usage_count: number;
  secrets: CredentialSecretMetadata[];
  created_at: string;
  updated_at: string;
}

export type PutCredentialSecretRequest =
  | { value_base64: string }
  | { certificate_pem_base64: string }
  | { certificate_pem_base64: string; private_key_pem_base64: string };
