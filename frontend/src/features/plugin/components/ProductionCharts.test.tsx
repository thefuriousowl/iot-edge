// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ProductionBarChart, { maximumProductionBarPoints } from "./ProductionBarChart";
import ProductionDonutChart from "./ProductionDonutChart";
import { collapseDonutData } from "./productionChartData";
import { expectNoAxeViolations } from "../../../test/axe";

const barChartSpy = vi.fn(); const barSpy = vi.fn(); const pieSpy = vi.fn();
vi.mock("recharts", () => { const Container = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>; const Empty = () => null; return { ResponsiveContainer: Container, BarChart: ({ children, data }: { children?: React.ReactNode; data: unknown[] }) => { barChartSpy(data); return <div>{children}</div>; }, Bar: (props: { dataKey: string; stackId?: string }) => { barSpy(props); return null; }, PieChart: Container, Pie: ({ children, data }: { children?: React.ReactNode; data: unknown[] }) => { pieSpy(data); return <div>{children}</div>; }, Cell: Empty, CartesianGrid: Empty, Tooltip: Empty, XAxis: Empty, YAxis: Empty }; });

describe("production Bar and Donut charts", () => {
  afterEach(() => { cleanup(); vi.clearAllMocks(); });
  it("bounds Bar buckets and supports grouped and stacked series", () => { const data = Array.from({ length: 520 }, (_, index) => ({ at: String(index), first: index, second: index })); const { rerender } = render(<ProductionBarChart data={data} xKey="at" unit="kWh" ariaLabel="Energy bars" series={[{ key: "first", label: "First", color: "cyan" }, { key: "second", label: "Second", color: "purple" }]} />); expect(barChartSpy).toHaveBeenLastCalledWith(data.slice(-maximumProductionBarPoints)); expect(barSpy).toHaveBeenCalledWith(expect.objectContaining({ stackId: undefined })); rerender(<ProductionBarChart data={data} xKey="at" unit="kWh" ariaLabel="Energy bars" mode="stacked" series={[{ key: "first", label: "First", color: "cyan" }]} />); expect(barSpy).toHaveBeenLastCalledWith(expect.objectContaining({ stackId: "total" })); });
  it("collapses only the tail into Other without double counting", () => { const input = [{ name: "A", value: 10 }, { name: "B", value: 9 }, { name: "C", value: 8 }, { name: "D", value: 7 }, { name: "E", value: 6 }, { name: "F", value: 5 }, { name: "G", value: 4 }, { name: "bad", value: -1 }]; const result = collapseDonutData(input); expect(result).toHaveLength(6); expect(result.at(-1)).toEqual({ name: "Other", value: 9 }); expect(result.reduce((sum, item) => sum + item.value, 0)).toBe(49); expect(input).toHaveLength(8); });
  it("renders explicit no-data and a same-dimension legend", () => { const { rerender } = render(<ProductionDonutChart data={[]} unit="THB" ariaLabel="Cost breakdown" />); expect(screen.getByRole("status")).toHaveTextContent("No positive breakdown data"); rerender(<ProductionDonutChart data={[{ name: "Hour 1", value: 20 }]} unit="THB" ariaLabel="Cost breakdown" />); expect(screen.getByRole("img", { name: "Cost breakdown" })).toBeInTheDocument(); expect(screen.getByRole("list", { name: "Breakdown legend" })).toHaveTextContent("Hour 120 THB"); expect(pieSpy).toHaveBeenCalled(); });
  it("keeps the Donut legend outside image semantics", async () => { const { container } = render(<ProductionDonutChart data={[{ name: "Hour 1", value: 20 }]} unit="THB" ariaLabel="Cost breakdown" />); expect(screen.getByRole("group", { name: "Cost breakdown and legend" })).toBeInTheDocument(); await expectNoAxeViolations(container); });
});
