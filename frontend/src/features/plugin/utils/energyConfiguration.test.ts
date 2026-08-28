import { describe, expect, it } from "vitest";
import type { EnergyConfig, PluginInstance } from "../../../types/plugin";
import { energyConfigFromForm, energyFormFromPlugin } from "./energyConfiguration";

const instance = (config: EnergyConfig): PluginInstance => ({ id: "p1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1, config, runtime: { state: "stopped", started_at: null, last_transition_at: null, error: null }, created_at: "", updated_at: "" });
describe("Energy configuration compatibility", () => {
  it.each([
    { logger_id: "l1", electrical_power_tags: [{ tag_id: "e1", unit: "W" as const }, { tag_id: "e2", unit: "MW" as const }], thermal_power_tags: [{ tag_id: "t1", unit: "kW" as const }], timezone: "Asia/Bangkok", max_gap_seconds: 120, tariff: { mode: "flat" as const, currency: "THB", rate_per_kwh: 4.25 } },
    { logger_id: "l1", electrical_power_tags: [{ tag_id: "e1", unit: "kW" as const }], timezone: "UTC", max_gap_seconds: 60, tariff: { mode: "tag" as const, currency: "USD", tag_id: "rate1" } },
  ])("round-trips existing flat and Tag configurations without loss", (config) => { const form = energyFormFromPlugin(instance(config)); expect(energyConfigFromForm(form)).toEqual(config); form.electrical[0].tag_id = "changed"; expect(config.electrical_power_tags[0].tag_id).not.toBe("changed"); });
});
