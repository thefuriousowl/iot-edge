// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ProductionLineChart, { maximumProductionLinePoints } from "./ProductionLineChart";

const lineSpy = vi.fn(); const chartSpy = vi.fn(); const referenceSpy = vi.fn();
vi.mock("recharts", () => {
  const Container = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>;
  return { ResponsiveContainer: Container, LineChart: ({ children, data }: { children?: React.ReactNode; data: unknown[] }) => { chartSpy(data); return <div>{children}</div>; }, Line: (props: { dataKey: string; connectNulls: boolean }) => { lineSpy(props); return <span data-testid={`line-${props.dataKey}`} />; }, ReferenceLine: (props: { y: number; label: string }) => { referenceSpy(props); return <span data-testid="target" />; }, Brush: () => <span data-testid="brush" />, CartesianGrid: () => null, Tooltip: () => <span data-testid="tooltip" />, XAxis: () => null, YAxis: () => null };
});

describe("ProductionLineChart", () => {
  afterEach(() => { cleanup(); vi.clearAllMocks(); });
  it("bounds points and renders multi-series, crosshair tooltip, navigator and target", () => {
    const data = Array.from({ length: 550 }, (_, index) => ({ at: String(index), power: index, previous: index === 549 ? null : index - 1 }));
    render(<ProductionLineChart data={data} xKey="at" unit="kW" ariaLabel="Power trend" series={[{ key: "power", label: "Power", color: "cyan" }, { key: "previous", label: "Previous", color: "gray", dashed: true }]} targets={[{ value: 100, label: "Target" }]} />);
    expect(screen.getByRole("img", { name: "Power trend" })).toHaveTextContent(`${maximumProductionLinePoints} points`);
    expect(chartSpy).toHaveBeenLastCalledWith(data.slice(-maximumProductionLinePoints)); expect(screen.getByTestId("brush")).toBeInTheDocument(); expect(screen.getByTestId("tooltip")).toBeInTheDocument(); expect(referenceSpy).toHaveBeenCalledWith(expect.objectContaining({ y: 100, label: "Target" })); expect(lineSpy).toHaveBeenCalledWith(expect.objectContaining({ connectNulls: false }));
  });
  it("toggles a series without mutating the input", () => {
    const data = [{ at: "one", power: 1, previous: 2 }];
    render(<ProductionLineChart data={data} xKey="at" unit="kW" ariaLabel="Power trend" series={[{ key: "power", label: "Power", color: "cyan" }, { key: "previous", label: "Previous", color: "gray" }]} />);
    fireEvent.click(screen.getByRole("button", { name: "Previous" })); expect(screen.getByRole("button", { name: "Previous" })).toHaveAttribute("aria-pressed", "false"); expect(screen.queryByTestId("line-previous")).not.toBeInTheDocument(); expect(data).toEqual([{ at: "one", power: 1, previous: 2 }]);
  });
});
