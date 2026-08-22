export type VGatewayType = "modbus_tcp";

export type VGatewayConnectionStatus =
  | "stopped"
  | "connecting"
  | "connected"
  | "disconnected"
  | "error";

export type VGatewayHealthStatus = "healthy" | "unhealthy" | "unknown";

export interface ModbusTCPConfig {
  host: string;
  port: number;
  timeout: number;
  retry_count: number;
  retry_delay: number;
  keep_alive: boolean;
  reconnect_interval: number;
}

export interface ModbusTCPConfigInput {
  host: string;
  port: number;
  timeout?: number;
  retry_count?: number;
  retry_delay?: number;
  keep_alive?: boolean;
  reconnect_interval?: number;
}

export interface VGateway {
  id: string;
  name: string;
  type: VGatewayType;
  description: string | null;
  enabled: boolean;
  status: VGatewayConnectionStatus;
  config: ModbusTCPConfig;
  created_at: string;
  updated_at: string;
}

export interface VGatewayListItem
  extends Omit<VGateway, "config"> {
  device_count: number;
  last_activity?: string | null;
}

export interface VGatewayStatistics {
  request_count: number;
  error_count: number;
  bytes_received: number;
  avg_latency_ms: number | null;
}

export interface VGatewayHealth {
  status: VGatewayHealthStatus;
  last_check: string | null;
  latency_ms: number | null;
}

export interface CreateVGatewayRequest {
  name: string;
  type: VGatewayType;
  description?: string | null;
  enabled?: boolean;
  config: ModbusTCPConfigInput;
}

export interface UpdateVGatewayRequest {
  name?: string;
  description?: string | null;
  enabled?: boolean;
  config?: ModbusTCPConfigInput;
}

export interface VGatewayListParams {
  type?: VGatewayType;
  enabled?: boolean;
  page?: number;
  per_page?: number;
}

export interface VGatewayPagination {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

export interface VGatewayListResponse {
  data: VGatewayListItem[];
  pagination: VGatewayPagination;
}

export interface VGatewayConnectionActionResponse {
  message: string;
  status: VGatewayConnectionStatus;
}

export interface TestVGatewayConnectionRequest {
  unit_id?: number;
}

export interface TestVGatewayConfigRequest {
  type: VGatewayType;
  config: ModbusTCPConfigInput;
  options?: TestVGatewayConnectionRequest;
}

export type TestVGatewayConnectionResponse =
  | {
      success: true;
      latency_ms: number;
      message: string;
    }
  | {
      success: false;
      error: string;
      message: string;
    };

export interface VGatewayStatusResponse {
  id: string;
  status: VGatewayConnectionStatus;
  connected_at: string | null;
  last_activity: string | null;
  statistics: VGatewayStatistics;
  health: VGatewayHealth;
}

export type VGatewayErrorCode =
  | "VGW001"
  | "VGW002"
  | "VGW003"
  | "VGW004"
  | "VGW005"
  | "VGW010"
  | "VGW011"
  | "VGW012"
  | "VGW013"
  | "VGW014";

export interface VGatewayErrorResponse {
  error: {
    code: VGatewayErrorCode;
    message: string;
  };
}
