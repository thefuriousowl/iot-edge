export type DeviceType = "modbus_device";
export type DatasourceType = "modbus_read";
export type DatasourceStatus = "idle" | "monitoring" | "paused" | "error";

export interface ModbusDeviceConfig {
  unit_id: number;
  poll_interval_ms: number;
  request_timeout_ms: number | null;
}

export interface ModbusDatasourceConfig {
  function_code: 1 | 2 | 3 | 4;
  start_address: number;
  quantity: number;
  poll_interval_ms: number | null;
}

export interface Device {
  id: string;
  vgateway_id: string;
  name: string;
  type: DeviceType;
  description: string | null;
  enabled: boolean;
  config: ModbusDeviceConfig;
  datasource_count?: number;
  tag_count?: number;
  created_at: string;
  updated_at: string;
}

export interface DeviceInventoryItem extends Device {
  vgateway_name: string;
  vgateway_type: string;
  vgateway_enabled: boolean;
  datasource_count: number;
  tag_count: number;
}

export interface DeviceInventoryParams {
  vgateway_id?: string;
  type?: DeviceType;
  enabled?: boolean;
  search?: string;
  page?: number;
  per_page?: number;
}

export interface DeviceInventoryPagination {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

export interface DeviceInventoryResponse {
  data: DeviceInventoryItem[];
  pagination: DeviceInventoryPagination;
}

export interface Datasource {
  id: string;
  device_id: string;
  name: string;
  type: DatasourceType;
  description: string | null;
  enabled: boolean;
  config: ModbusDatasourceConfig;
  status: DatasourceStatus;
  created_at: string;
  updated_at: string;
}

export interface CreateDeviceRequest {
  name: string;
  type: DeviceType;
  description?: string | null;
  enabled?: boolean;
  config: {
    unit_id: number;
    poll_interval_ms?: number;
    request_timeout_ms?: number;
  };
}

export interface CreateDatasourceRequest {
  name: string;
  type: DatasourceType;
  description?: string | null;
  enabled?: boolean;
  config: {
    function_code: 1 | 2 | 3 | 4;
    start_address: number;
    quantity: number;
    poll_interval_ms?: number;
  };
}

export type UpdateDeviceRequest = Partial<Omit<CreateDeviceRequest, "type">>;
export type UpdateDatasourceRequest = Partial<Omit<CreateDatasourceRequest, "type">>;

export interface ModbusRegisterValue {
  address: number;
  value: number;
  hex: string;
}

export interface ModbusBitValue {
  address: number;
  value: boolean;
}

export interface DatasourceSample {
  datasource_id: string;
  sequence: number;
  observed_at: string;
  latency_ms: number;
  quality: "good" | "bad" | "uncertain";
  raw_hex: string;
  data: {
    registers?: ModbusRegisterValue[];
    bits?: ModbusBitValue[];
    [key: string]: unknown;
  };
  error?: string;
}
