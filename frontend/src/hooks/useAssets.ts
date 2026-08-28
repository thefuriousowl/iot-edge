import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  createAsset, deleteAsset, getAsset, getAssetAncestors, getAssetBindings, getAssetChildren,
  getAssetConnectivity, getAssetMeasurements, getAssetRoots, getAssetTree, getTagAssets,
  listAssets, moveAsset, replaceAssetBindings, updateAsset,
} from "../services/asset.service";
import type {
  AssetListParams, CreateAssetRequest, MoveAssetRequest, ReplaceAssetBindingsRequest,
  UpdateAssetRequest,
} from "../types/asset";

const normalizedList = (params?: AssetListParams) => ({
  search: params?.search ?? "",
  kind: params?.kind ?? null,
  enabled: params?.enabled ?? null,
  page: params?.page ?? 1,
  per_page: params?.per_page ?? 20,
});

export const assetKeys = {
  all: ["assets"] as const,
  lists: () => [...assetKeys.all, "list"] as const,
  list: (params?: AssetListParams) => [...assetKeys.lists(), normalizedList(params)] as const,
  roots: () => [...assetKeys.all, "roots"] as const,
  details: () => [...assetKeys.all, "detail"] as const,
  detail: (id: string) => [...assetKeys.details(), id] as const,
  children: (id: string) => [...assetKeys.detail(id), "children"] as const,
  tree: (id: string) => [...assetKeys.detail(id), "tree"] as const,
  ancestors: (id: string) => [...assetKeys.detail(id), "ancestors"] as const,
  bindings: (id: string) => [...assetKeys.detail(id), "bindings"] as const,
  measurements: (id: string) => [...assetKeys.detail(id), "measurements"] as const,
  connectivity: (id: string) => [...assetKeys.detail(id), "connectivity"] as const,
  tagAssets: (tagID: string) => [...assetKeys.all, "tag", tagID, "assets"] as const,
};

export const useAssets = (params?: AssetListParams) => useQuery({ queryKey: assetKeys.list(params), queryFn: ({ signal }) => listAssets(params, signal) });
export const useAssetRoots = () => useQuery({ queryKey: assetKeys.roots(), queryFn: ({ signal }) => getAssetRoots(signal) });
export const useAsset = (id: string) => useQuery({ queryKey: assetKeys.detail(id), queryFn: ({ signal }) => getAsset(id, signal), enabled: id.length > 0 });
export const useAssetChildren = (id: string) => useQuery({ queryKey: assetKeys.children(id), queryFn: ({ signal }) => getAssetChildren(id, signal), enabled: id.length > 0 });
export const useAssetTree = (id: string) => useQuery({ queryKey: assetKeys.tree(id), queryFn: ({ signal }) => getAssetTree(id, signal), enabled: id.length > 0 });
export const useAssetAncestors = (id: string) => useQuery({ queryKey: assetKeys.ancestors(id), queryFn: ({ signal }) => getAssetAncestors(id, signal), enabled: id.length > 0 });
export const useAssetBindings = (id: string) => useQuery({ queryKey: assetKeys.bindings(id), queryFn: ({ signal }) => getAssetBindings(id, signal), enabled: id.length > 0 });
export const useAssetMeasurements = (id: string) => useQuery({ queryKey: assetKeys.measurements(id), queryFn: ({ signal }) => getAssetMeasurements(id, signal), enabled: id.length > 0 });
export const useAssetConnectivity = (id: string) => useQuery({ queryKey: assetKeys.connectivity(id), queryFn: ({ signal }) => getAssetConnectivity(id, signal), enabled: id.length > 0 });
export const useTagAssets = (tagID: string) => useQuery({ queryKey: assetKeys.tagAssets(tagID), queryFn: ({ signal }) => getTagAssets(tagID, signal), enabled: tagID.length > 0 });

function useInvalidateAssets() {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: assetKeys.all });
}

export function useCreateAsset() {
  const invalidate = useInvalidateAssets();
  return useMutation({ mutationFn: (data: CreateAssetRequest) => createAsset(data), onSuccess: invalidate });
}

export function useUpdateAsset() {
  const invalidate = useInvalidateAssets();
  return useMutation({ mutationFn: ({ id, data }: { id: string; data: UpdateAssetRequest }) => updateAsset(id, data), onSuccess: invalidate });
}

export function useMoveAsset() {
  const invalidate = useInvalidateAssets();
  return useMutation({ mutationFn: ({ id, data }: { id: string; data: MoveAssetRequest }) => moveAsset(id, data), onSuccess: invalidate });
}

export function useDeleteAsset() {
  const invalidate = useInvalidateAssets();
  return useMutation({ mutationFn: (id: string) => deleteAsset(id), onSuccess: invalidate });
}

export function useReplaceAssetBindings() {
  const invalidate = useInvalidateAssets();
  return useMutation({ mutationFn: ({ id, data }: { id: string; data: ReplaceAssetBindingsRequest }) => replaceAssetBindings(id, data), onSuccess: invalidate });
}
