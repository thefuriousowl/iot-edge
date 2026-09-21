import { Cell, Pie, PieChart, ResponsiveContainer, Tooltip } from "recharts";
import { collapseDonutData, type DonutDatum } from "./productionChartData";
const colors = ["#34c3ff", "#8d7cf6", "#55c6a9", "#d9b76e", "#ef8d99", "#718392"];

function ProductionDonutChart({ data, ariaLabel, unit }: { data: DonutDatum[]; ariaLabel: string; unit: string }) {
  const collapsed = collapseDonutData(data);
  if (!collapsed.length) return <div className="production-chart-empty" role="status">No positive breakdown data in this range.</div>;
  return <div className="production-donut" role="group" aria-label={`${ariaLabel} and legend`}>
    <div className="production-donut-canvas" role="img" aria-label={ariaLabel}><ResponsiveContainer width="100%" height="100%"><PieChart><Tooltip formatter={(value) => [`${Number(value).toLocaleString()} ${unit}`, "Value"]} /><Pie data={collapsed} dataKey="value" nameKey="name" innerRadius="48%" outerRadius="78%">{collapsed.map((item, index) => <Cell key={item.name} fill={colors[index % colors.length]} />)}</Pie></PieChart></ResponsiveContainer></div>
    <ul aria-label="Breakdown legend">{collapsed.map((item, index) => <li key={item.name}><i aria-hidden="true" style={{ background: colors[index % colors.length] }} /><span>{item.name}</span><strong>{item.value.toLocaleString()} {unit}</strong></li>)}</ul>
  </div>;
}
export default ProductionDonutChart;
