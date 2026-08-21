import { describe, expect, expectTypeOf, it } from "vitest";

import type {
  CreateVGatewayRequest,
  TestVGatewayConnectionResponse,
  UpdateVGatewayRequest,
  VGateway,
  VGatewayConnectionStatus,
  VGatewayListResponse,
} from "./vgateway";

const gateway: VGateway = {
  id: "550e8400-e29b-41d4-a716-446655440000",
  name: "Main PLC Gateway",
  type: "modbus_tcp",
  description: "Connection to main factory PLC",
  enabled: true,
  status: "disconnected",
  config: {
    host: "192.168.1.100",
    port: 502,
    timeout: 5_000,
    retry_count: 3,
    retry_delay: 1_000,
    keep_alive: true,
    reconnect_interval: 30,
  },
  created_at: "2026-08-21T08:00:00Z",
  updated_at: "2026-08-21T08:00:00Z",
};

describe("vGateway types", () => {
  it("models a normalized ModbusTCP gateway response", () => {
    expect(gateway.type).toBe("modbus_tcp");
    expect(gateway.config.port).toBe(502);
    expectTypeOf(gateway.status).toEqualTypeOf<VGatewayConnectionStatus>();
  });

  it("keeps server-defaulted connection settings optional on create", () => {
    const request = {
      name: "Main PLC Gateway",
      type: "modbus_tcp",
      config: {
        host: "192.168.1.100",
        port: 502,
      },
    } satisfies CreateVGatewayRequest;

    expect(request.config.host).toBe("192.168.1.100");
    expectTypeOf<UpdateVGatewayRequest>().not.toHaveProperty("type");
  });

  it("models paginated lists and both connection-test outcomes", () => {
    expectTypeOf<VGatewayListResponse["data"]>().toBeArray();

    const success: TestVGatewayConnectionResponse = {
      success: true,
      latency_ms: 15,
      message: "Connection successful",
    };
    const failure: TestVGatewayConnectionResponse = {
      success: false,
      error: "Connection refused",
      message: "Unable to connect to 192.168.1.100:502",
    };

    expect(success.success).toBe(true);
    expect(failure.success).toBe(false);
  });
});
