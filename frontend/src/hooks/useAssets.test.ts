import { describe, expect, it } from "vitest";

import { assetKeys } from "./useAssets";

describe("Asset query keys", () => {
  it("normalizes defaults and property order into stable list keys", () => {
    expect(assetKeys.list()).toEqual(assetKeys.list({ page: 1, per_page: 20 }));
    expect(assetKeys.list({ enabled: false, kind: "meter", search: "main", per_page: 10, page: 2 }))
      .toEqual(assetKeys.list({ page: 2, search: "main", kind: "meter", per_page: 10, enabled: false }));
  });

  it("keeps resource projections under one invalidation boundary", () => {
    expect(assetKeys.bindings("a").slice(0, 3)).toEqual(["assets", "detail", "a"]);
    expect(assetKeys.measurements("a").slice(0, 3)).toEqual(["assets", "detail", "a"]);
    expect(assetKeys.connectivity("a").slice(0, 3)).toEqual(["assets", "detail", "a"]);
    expect(assetKeys.tagAssets("t")[0]).toBe("assets");
  });
});
