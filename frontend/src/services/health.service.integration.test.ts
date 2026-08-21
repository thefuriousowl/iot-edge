import { describe, expect, it } from "vitest";

import { getHealth } from "./health.service";

const integrationTest =
  import.meta.env.VITE_RUN_INTEGRATION_TESTS === "true" ? it : it.skip;

describe("Health service integration", () => {
  integrationTest("fetches health data from the backend", async () => {
    const health = await getHealth();

    expect(health).toEqual({
      status: "ok",
      version: "0.1.0",
    });
  });
});
