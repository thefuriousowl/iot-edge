import type {
  CreateVGatewayRequest,
  TestVGatewayConfigRequest,
  TestVGatewayConnectionRequest,
  TestVGatewayConnectionResponse,
  UpdateVGatewayRequest,
  VGateway,
  VGatewayConnectionActionResponse,
  VGatewayDetail,
  VGatewayListParams,
  VGatewayListResponse,
  VGatewayStatusResponse,
} from "../types/vgateway";
import api from "./api";

function vGatewayPath(id: string): string {
  return `/vgateways/${encodeURIComponent(id)}`;
}

export async function listVGateways(
  params?: VGatewayListParams,
  signal?: AbortSignal,
): Promise<VGatewayListResponse> {
  const response = await api.get<VGatewayListResponse>("/vgateways", {
    params,
    signal,
  });

  return response.data;
}

export async function createVGateway(
  data: CreateVGatewayRequest,
): Promise<VGateway> {
  const response = await api.post<VGateway>("/vgateways", data);

  return response.data;
}

export async function getVGateway(
  id: string,
  signal?: AbortSignal,
): Promise<VGatewayDetail> {
  const response = await api.get<VGatewayDetail>(vGatewayPath(id), { signal });

  return response.data;
}

export async function updateVGateway(
  id: string,
  data: UpdateVGatewayRequest,
): Promise<VGateway> {
  const response = await api.put<VGateway>(vGatewayPath(id), data);

  return response.data;
}

export async function deleteVGateway(id: string): Promise<void> {
  await api.delete(vGatewayPath(id));
}

export async function connectVGateway(
  id: string,
): Promise<VGatewayConnectionActionResponse> {
  const response = await api.post<VGatewayConnectionActionResponse>(
    `${vGatewayPath(id)}/connect`,
  );

  return response.data;
}

export async function disconnectVGateway(
  id: string,
): Promise<VGatewayConnectionActionResponse> {
  const response = await api.post<VGatewayConnectionActionResponse>(
    `${vGatewayPath(id)}/disconnect`,
  );

  return response.data;
}

export async function testVGatewayConnection(
  id: string,
  data?: TestVGatewayConnectionRequest,
): Promise<TestVGatewayConnectionResponse> {
  const response = await api.post<TestVGatewayConnectionResponse>(
    `${vGatewayPath(id)}/test`,
    data,
  );

  return response.data;
}

export async function testVGatewayConfig(
  data: TestVGatewayConfigRequest,
): Promise<TestVGatewayConnectionResponse> {
  const response = await api.post<TestVGatewayConnectionResponse>(
    "/vgateways/test",
    data,
  );

  return response.data;
}

export async function getVGatewayStatus(
  id: string,
  signal?: AbortSignal,
): Promise<VGatewayStatusResponse> {
  const response = await api.get<VGatewayStatusResponse>(
    `${vGatewayPath(id)}/status`,
    { signal },
  );

  return response.data;
}
