import type { TagValue } from "./tag";

export type AssetKind = "site" | "building" | "area" | "system" | "equipment" | "meter" | "custom";
export type AssetResource = "electricity" | "thermal" | "compressed_air" | "steam" | "gas" | "water" | "solar" | "custom";
export type AssetQuantity = "power" | "energy" | "flow_rate" | "volume" | "pressure" | "temperature" | "ratio" | "state" | "cost";
export type AssetUnit = "W" | "kW" | "MW" | "Wh" | "kWh" | "MWh" | "m3" | "Nm3" | "m3/s" | "m3/h" | "Nm3/h" | "Pa" | "kPa" | "bar" | "K" | "degC" | "degF" | "1" | "%" | "bool";

export interface Asset {
  id: string;
  parent_id: string | null;
  name: string;
  kind: AssetKind;
  description: string | null;
  enabled: boolean;
  timezone: string | null;
  position: number;
  metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface AssetTreeNode extends Asset {
  depth: number;
  effective_enabled: boolean;
  measurement_count: number;
  children: AssetTreeNode[];
}

export interface AssetListParams {
  search?: string;
  kind?: AssetKind;
  enabled?: boolean;
  page?: number;
  per_page?: number;
}

export interface AssetListResponse {
  data: Asset[];
  pagination: { page: number; per_page: number; total: number; total_pages: number };
}

export interface AssetCollectionResponse { data: Asset[] }

export interface CreateAssetRequest {
  parent_id?: string | null;
  name: string;
  kind: AssetKind;
  description?: string | null;
  enabled?: boolean;
  timezone?: string | null;
  position?: number;
  metadata?: Record<string, unknown>;
}

export interface UpdateAssetRequest {
  name?: string;
  kind?: AssetKind;
  description?: string | null;
  enabled?: boolean;
  timezone?: string | null;
  position?: number;
  metadata?: Record<string, unknown>;
}

export interface MoveAssetRequest { parent_id: string | null; position: number }

export interface AssetReferenceCondition {
  volume_basis?: "actual" | "normalized";
  pressure_basis?: "absolute" | "gauge";
  temperature_kelvin?: number;
  pressure_pascal?: number;
}

export interface AssetSemantic {
  resource: AssetResource;
  quantity: AssetQuantity;
  unit: AssetUnit;
  precision: number;
  reference?: AssetReferenceCondition;
}

export type AssetSourceReference =
  | { kind: "tag"; tag_id: string; plugin_instance_id?: never; output_key?: never }
  | { kind: "plugin_output"; tag_id?: never; plugin_instance_id: string; output_key: string };

export interface MeasurementBinding {
  id: string;
  owner_asset_id: string;
  boundary_asset_id: string;
  source_key: string;
  source: AssetSourceReference;
  semantic: AssetSemantic;
  meter_role: "direct" | "main" | "submeter" | "virtual";
  rollup_policy: "include" | "exclude";
  virtual_inputs?: string[];
}

export interface AssetBindingsResponse { data: MeasurementBinding[] }
export type MeasurementBindingInput = Pick<MeasurementBinding, "boundary_asset_id" | "source" | "semantic" | "meter_role" | "rollup_policy"> & { id?: string; virtual_inputs?: string[] };
export interface ReplaceAssetBindingsRequest { bindings: MeasurementBindingInput[] }

export interface AssetMeasurementReading {
  binding_id: string;
  source: AssetSourceReference;
  semantic: AssetSemantic;
  available: boolean;
  sequence?: number;
  value: TagValue | string | null;
  quality: string;
  error?: string;
  observed_at?: string;
  emitted_at?: string;
  period_start?: string;
  period_end?: string;
}

export interface AssetMeasurementProjection {
  binding: MeasurementBinding;
  latest: AssetMeasurementReading;
  history: AssetMeasurementReading[];
  history_retention: "runtime_memory" | "latest_only";
}

export interface AssetMeasurementSnapshot {
  asset_id: string;
  captured_at: string;
  measurements: AssetMeasurementProjection[];
}

export interface ConnectivityEntity { id: string; name: string; kind: string; enabled: boolean }
export interface AssetConnectivityLink {
  binding_id: string;
  source: AssetSourceReference;
  tag?: ConnectivityEntity;
  datasource?: ConnectivityEntity;
  device?: ConnectivityEntity;
  vgateway?: ConnectivityEntity;
}
export interface AssetConnectivity { asset_id: string; links: AssetConnectivityLink[] }
export interface TagAssetLink { binding_id: string; asset: Asset; semantic: AssetSemantic }
export interface TagAssetConnectivity { tag_id: string; assets: TagAssetLink[] }

export type AssetErrorCode = "ASSET004" | "ASSET001" | "ASSET_DEPENDENTS" | "ASSET_SOURCE004" | "ASSET_SOURCE_EXISTS" | "ASSET_LIVE_EMPTY" | "ASSET_LIVE_UNAVAILABLE" | "ASSET_CONNECTIVITY_SOURCE004" | "ASSET_CONNECTIVITY_INCOMPLETE" | "ASSET_CONNECTIVITY_UNAVAILABLE" | "VALIDATION_ERROR" | "INTERNAL_ERROR";
export interface AssetErrorResponse { error: { code: AssetErrorCode; message: string } }
