import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

export interface ProductionBarSeries { key: string; label: string; color: string }
export const maximumProductionBarPoints = 500;

function ProductionBarChart({ data, xKey, series, mode = "grouped", ariaLabel, unit }: { data: Array<Record<string, string | number | null>>; xKey: string; series: ProductionBarSeries[]; mode?: "grouped" | "stacked"; ariaLabel: string; unit: string }) {
  const bounded = data.slice(-maximumProductionBarPoints);
  if (!bounded.length || !series.length) return <div className="production-chart-empty" role="status">No bar-chart data in this range.</div>;
  return <div className="production-bar" role="img" aria-label={ariaLabel}><ResponsiveContainer width="100%" height="100%"><BarChart data={bounded}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey={xKey} minTickGap={28} tick={{ fill: "#8295a4", fontSize: 10 }} /><YAxis unit={unit} tick={{ fill: "#8295a4", fontSize: 10 }} /><Tooltip cursor={{ fill: "rgba(159,180,194,.06)" }} />{series.map((item) => <Bar key={item.key} dataKey={item.key} name={item.label} fill={item.color} stackId={mode === "stacked" ? "total" : undefined} radius={[3, 3, 0, 0]} />)}</BarChart></ResponsiveContainer><small>{bounded.length} buckets · {mode} · bounded to {maximumProductionBarPoints}</small></div>;
}

export default ProductionBarChart;
