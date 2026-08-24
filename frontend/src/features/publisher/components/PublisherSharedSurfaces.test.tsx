// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createRef, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MQTTPublisherDraft, PublisherSourceCatalogEntry, PublisherSourceDraft } from "../../../types/publisher";
import PublisherSourceSelector from "./PublisherSourceSelector";
import PublisherTemplateEditor from "./PublisherTemplateEditor";
import PublisherTriggerSetup from "./PublisherTriggerSetup";

const catalog: PublisherSourceCatalogEntry[] = [{
  descriptor: {
    reference: { kind: "tag", tag_id: "tag-1" }, name: "Active power", owner_name: "Power meter",
    schema_version: 1, data_type: "float64", unit: "kW", period_kind: "instantaneous", enabled: true,
  },
  current: { quality: "good", sequence: 4, observed_at: "2026-08-23T09:00:00Z" },
}];

const sources: PublisherSourceDraft[] = [{
  id: "source-1", alias: "active_power", name: "Active power", owner_name: "Power meter",
  kind: "tag", data_type: "float64", unit: "kW", period_kind: "instantaneous",
  reference: { kind: "tag", tag_id: "tag-1" },
}];

afterEach(() => cleanup());

describe("PublisherSourceSelector", () => {
  it("shares immutable catalog selection, filtering, aliases, and timing guidance", () => {
    const onSearchChange = vi.fn();
    const onKindChange = vi.fn();
    const onAdd = vi.fn();
    const onUpdate = vi.fn();
    const onRemove = vi.fn();
    render(<PublisherSourceSelector
      catalog={catalog} catalogState="ready" search="" kind="" sources={sources}
      onSearchChange={onSearchChange} onKindChange={onKindChange} onAdd={onAdd} onUpdate={onUpdate} onRemove={onRemove}
    />);

    expect(screen.getByText("Sources are asynchronous.")).toBeInTheDocument();
    expect(screen.getByText(/does not align timestamps or initiate acquisition/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Selected" })).toBeDisabled();
    expect(screen.getByLabelText("Display name for active_power")).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Search Publisher sources"), { target: { value: "energy" } });
    fireEvent.change(screen.getByLabelText("Publisher source kind"), { target: { value: "plugin_output" } });
    fireEvent.change(screen.getByLabelText("Alias for Active power"), { target: { value: "meter_kw" } });
    fireEvent.click(screen.getByRole("button", { name: "Remove source active_power" }));

    expect(onSearchChange).toHaveBeenCalledWith("energy");
    expect(onKindChange).toHaveBeenCalledWith("plugin_output");
    expect(onUpdate).toHaveBeenCalledWith("source-1", { alias: "meter_kw" });
    expect(onRemove).toHaveBeenCalledWith("source-1");
  });

  it("adds an unselected catalog source", () => {
    const onAdd = vi.fn();
    render(<PublisherSourceSelector catalog={catalog} catalogState="ready" search="" kind="" sources={[]}
      onSearchChange={vi.fn()} onKindChange={vi.fn()} onAdd={onAdd} onUpdate={vi.fn()} onRemove={vi.fn()} />);
    const row = screen.getByText("Active power").closest("article");
    fireEvent.click(within(row!).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith(catalog[0]);
  });

  it("preserves legacy rows for repair and exposes every editable descriptor field", () => {
    const onUpdate = vi.fn();
    const legacySource: PublisherSourceDraft = {
      ...sources[0],
      id: "legacy-1",
      alias: "legacy_power",
      kind: "plugin_output",
      period_kind: "windowed",
      reference: undefined,
    };

    render(<PublisherSourceSelector catalog={[]} catalogState="error" search="power" kind="plugin_output" sources={[legacySource]}
      onSearchChange={vi.fn()} onKindChange={vi.fn()} onAdd={vi.fn()} onUpdate={onUpdate} onRemove={vi.fn()} />);

    expect(screen.getByText(/legacy schema-only rows/)).toBeInTheDocument();
    expect(screen.getByText(/Source catalog unavailable/)).toBeInTheDocument();
    expect(screen.getByLabelText("Display name for legacy_power")).toBeEnabled();

    fireEvent.change(screen.getByLabelText("Display name for legacy_power"), { target: { value: "Legacy demand" } });
    fireEvent.change(screen.getByLabelText("Kind for legacy_power"), { target: { value: "tag" } });
    fireEvent.change(screen.getByLabelText("Data type for legacy_power"), { target: { value: "float32" } });
    fireEvent.change(screen.getByLabelText("Unit for legacy_power"), { target: { value: "W" } });
    fireEvent.change(screen.getByLabelText("Period for legacy_power"), { target: { value: "instantaneous" } });

    expect(onUpdate).toHaveBeenCalledWith("legacy-1", { name: "Legacy demand" });
    expect(onUpdate).toHaveBeenCalledWith("legacy-1", { kind: "tag", period_kind: "instantaneous" });
    expect(onUpdate).toHaveBeenCalledWith("legacy-1", { data_type: "float32" });
    expect(onUpdate).toHaveBeenCalledWith("legacy-1", { unit: "W" });
    expect(onUpdate).toHaveBeenCalledWith("legacy-1", { period_kind: "instantaneous" });
  });

  it("distinguishes loading and empty ready catalog states", () => {
    const props = {
      catalog: [], search: "", kind: "" as const, sources: [],
      onSearchChange: vi.fn(), onKindChange: vi.fn(), onAdd: vi.fn(), onUpdate: vi.fn(), onRemove: vi.fn(),
    };
    const { rerender } = render(<PublisherSourceSelector {...props} catalogState="loading" />);

    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByText(/No enabled sources/)).not.toBeInTheDocument();

    rerender(<PublisherSourceSelector {...props} catalogState="ready" />);
    expect(screen.getByText("No enabled sources match this filter.")).toBeInTheDocument();
  });
});

describe("PublisherTriggerSetup", () => {
  it("makes asynchronous interval and on-change semantics explicit", () => {
    const triggerSources = [
      sources[0],
      { ...sources[0], id: "source-2", alias: "thermal_kw", name: "Thermal power" },
    ];

    function Harness() {
      const [trigger, setTrigger] = useState<MQTTPublisherDraft["trigger"]>({ mode: "interval", interval_ms: 60_000, source_alias: "stale_alias", coalesce_ms: 500 });
      return <PublisherTriggerSetup trigger={trigger} sources={triggerSources} onChange={setTrigger} />;
    }
    render(<Harness />);

    expect(screen.getByText(/never changes Datasource polling or requests a Device/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Trigger interval"), { target: { value: "5000" } });
    expect(screen.getByLabelText("Trigger interval")).toHaveValue(5000);
    fireEvent.click(screen.getByRole("button", { name: /On source change/ }));
    expect(screen.getByText(/retaining other sources at their own timestamps/)).toBeInTheDocument();
    expect(screen.getByLabelText("Trigger source")).toHaveValue("active_power");
    fireEvent.change(screen.getByLabelText("Trigger source"), { target: { value: "thermal_kw" } });
    expect(screen.getByLabelText("Trigger source")).toHaveValue("thermal_kw");
    fireEvent.change(screen.getByLabelText("Coalesce window"), { target: { value: "250" } });
    expect(screen.getByLabelText("Coalesce window")).toHaveValue(250);
    fireEvent.click(screen.getByRole("button", { name: /Fixed interval/ }));
    expect(screen.getByLabelText("Trigger interval")).toHaveValue(5000);
  });
});

describe("PublisherTemplateEditor", () => {
  it("shares free-form editing, helper insertion, fixtures, and Core validation", () => {
    const onTemplateChange = vi.fn();
    const onFixtureChange = vi.fn();
    const onInsert = vi.fn();
    const onValidate = vi.fn();
    render(<PublisherTemplateEditor
      editorRef={createRef<HTMLTextAreaElement>()} editorID="payload" editorLabel="Advanced payload template"
      template={'{"power":{{value "active_power"}}}'} sources={sources} fixture="good"
      preview={{ result: { rendered: '{"power":42}', helperCalls: 1, referencedAliases: ["active_power"] }, error: "" }}
      validation={null} onTemplateChange={onTemplateChange} onFixtureChange={onFixtureChange}
      onInsert={onInsert} onValidate={onValidate}
    />);

    fireEvent.change(screen.getByLabelText("Advanced payload template"), { target: { value: "{}" } });
    fireEvent.change(screen.getByLabelText("Payload source helper"), { target: { value: "quality" } });
    fireEvent.click(screen.getByRole("button", { name: "Insert quality syntax for active_power" }));
    fireEvent.click(screen.getByRole("tab", { name: "Unavailable" }));
    fireEvent.click(screen.getByRole("button", { name: "Validate with Core" }));

    expect(onTemplateChange).toHaveBeenCalledWith("{}");
    expect(onInsert).toHaveBeenCalledWith('{{quality "active_power"}}');
    expect(onFixtureChange).toHaveBeenCalledWith("unavailable");
    expect(onValidate).toHaveBeenCalledOnce();
    expect(screen.getByText('{"power":42}')).toBeInTheDocument();
  });
});
