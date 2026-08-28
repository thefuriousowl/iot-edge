import { useMemo, useState } from "react";
import { Brush, CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

export interface ProductionLineSeries { key: string; label: string; color: string; dashed?: boolean }
export interface ProductionLineTarget { value: number; label: string; color?: string }

interface Props {
  data: Array<Record<string, string | number | null>>;
  xKey: string;
  series: ProductionLineSeries[];
  targets?: ProductionLineTarget[];
  ariaLabel: string;
  unit: string;
}

export const maximumProductionLinePoints = 500;

function ProductionLineChart({ data, xKey, series, targets = [], ariaLabel, unit }: Props) {
  const [hidden, setHidden] = useState<Set<string>>(() => new Set());
  const bounded = useMemo(() => data.slice(-maximumProductionLinePoints), [data]);
  const visible = series.filter((item) => !hidden.has(item.key));
  const toggle = (key: string) => setHidden((current) => { const next = new Set(current); if (next.has(key)) next.delete(key); else next.add(key); return next; });

  return <div className="production-line" role="img" aria-label={ariaLabel}>
    <div className="production-line-legend" role="group" aria-label="Chart series">{series.map((item) => <button type="button" key={item.key} aria-pressed={!hidden.has(item.key)} onClick={() => toggle(item.key)}><i style={{ background: item.color }} />{item.label}</button>)}</div>
    <div className="production-line-canvas"><ResponsiveContainer width="100%" height="100%"><LineChart data={bounded}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey={xKey} minTickGap={28} tick={{ fill: "#8295a4", fontSize: 10 }} /><YAxis unit={unit} tick={{ fill: "#8295a4", fontSize: 10 }} /><Tooltip cursor={{ stroke: "#9fb4c2", strokeDasharray: "3 3" }} />{targets.map((target) => <ReferenceLine key={`${target.label}-${target.value}`} y={target.value} label={target.label} stroke={target.color ?? "#d9b76e"} strokeDasharray="5 4" />)}{visible.map((item) => <Line key={item.key} type="monotone" dataKey={item.key} name={item.label} stroke={item.color} strokeWidth={2} strokeDasharray={item.dashed ? "6 4" : undefined} dot={false} connectNulls={false} />)}<Brush dataKey={xKey} height={24} travellerWidth={8} stroke="#526b7d" /></LineChart></ResponsiveContainer></div>
    <small>{bounded.length} points · bounded to {maximumProductionLinePoints} · drag the navigator to zoom or pan</small>
  </div>;
}

export default ProductionLineChart;
