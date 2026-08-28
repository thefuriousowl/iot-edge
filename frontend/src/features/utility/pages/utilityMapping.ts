export type SourceKind = "tag" | "plugin_output";
export type Resource = "electricity" | "thermal" | "compressed_air" | "water";
export const utilityUnits: Record<Resource, string[]> = { electricity: ["kW", "kWh"], thermal: ["kW", "kWh", "degC"], compressed_air: ["kW", "Nm3/h", "Nm3", "bar", "degC", "bool"], water: ["m3/h", "m3"] };
export const utilityQuantities: Record<Resource, string[]> = { electricity: ["power", "energy"], thermal: ["power", "energy", "temperature"], compressed_air: ["power", "flow rate", "volume", "pressure", "temperature", "state"], water: ["flow rate", "volume"] };
export interface UtilityDraft { assetID: string; loggerID: string; resource: Resource; sourceKind: SourceKind; sourceID: string; quantity: string; unit: string; tariffMode: "none" | "fixed" | "source"; currency: string; rate: number }
export function validateUtilityDraft(value: UtilityDraft): string[] {
  const errors: string[] = [];
  if (!value.assetID) errors.push("Choose the Asset that owns this utility boundary.");
  if (!value.loggerID) errors.push("Choose the Data Logger that provides synchronized history.");
  if (!value.sourceID.trim()) errors.push(value.sourceKind === "tag" ? "Choose a Logger Tag." : "Enter a Plugin output key.");
  if (!utilityQuantities[value.resource].includes(value.quantity)) errors.push("Choose a measurement meaning supported by this resource.");
  if (!utilityUnits[value.resource].includes(value.unit)) errors.push("Choose a unit supported by this resource.");
  if (value.tariffMode !== "none" && !/^[A-Z]{3}$/.test(value.currency)) errors.push("Currency must be a three-letter code such as THB.");
  if (value.tariffMode === "fixed" && (!Number.isFinite(value.rate) || value.rate < 0)) errors.push("Fixed tariff cannot be negative.");
  return errors;
}
