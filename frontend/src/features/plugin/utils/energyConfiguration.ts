import type { EnergyConfig, EnergyPowerTag, PluginInstance } from "../../../types/plugin";

export interface EnergyConfigurationForm {
  name: string; enabled: boolean; loggerID: string; electrical: EnergyPowerTag[]; thermal: EnergyPowerTag[];
  timezone: string; maxGapSeconds: number; currency: string; tariffMode: "flat" | "tag"; ratePerKWh: number; tariffTagID: string;
}

export function energyFormFromPlugin(instance: PluginInstance): EnergyConfigurationForm {
  const config = instance.config as EnergyConfig;
  return { name: instance.name, enabled: instance.enabled, loggerID: config.logger_id,
    electrical: config.electrical_power_tags.map((mapping) => ({ ...mapping })), thermal: (config.thermal_power_tags ?? []).map((mapping) => ({ ...mapping })),
    timezone: config.timezone, maxGapSeconds: config.max_gap_seconds, currency: config.tariff.currency,
    tariffMode: config.tariff.mode === "tag" ? "tag" : "flat", ratePerKWh: config.tariff.mode === "tag" ? 0 : config.tariff.rate_per_kwh,
    tariffTagID: config.tariff.mode === "tag" ? config.tariff.tag_id : "" };
}

export function energyConfigFromForm(form: EnergyConfigurationForm): EnergyConfig {
  return { logger_id: form.loggerID, electrical_power_tags: form.electrical.map((mapping) => ({ ...mapping })),
    ...(form.thermal.length > 0 ? { thermal_power_tags: form.thermal.map((mapping) => ({ ...mapping })) } : {}), timezone: form.timezone,
    max_gap_seconds: form.maxGapSeconds, tariff: form.tariffMode === "tag" ? { mode: "tag", currency: form.currency, tag_id: form.tariffTagID } : { mode: "flat", currency: form.currency, rate_per_kwh: form.ratePerKWh } };
}
