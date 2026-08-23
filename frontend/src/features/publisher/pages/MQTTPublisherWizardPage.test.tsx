// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import MQTTPublisherWizardPage from "./MQTTPublisherWizardPage";

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet online</span>,
}));

function renderPage() {
  return render(<MemoryRouter><MQTTPublisherWizardPage /></MemoryRouter>);
}

describe("MQTTPublisherWizardPage", () => {
  beforeEach(() => {
    const values = new Map<string, string>();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        clear: () => values.clear(),
        getItem: (key: string) => values.get(key) ?? null,
        key: (index: number) => [...values.keys()][index] ?? null,
        get length() { return values.size; },
        removeItem: (key: string) => values.delete(key),
        setItem: (key: string, value: string) => values.set(key, value),
      },
    });
    localStorage.clear();
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  });

  afterEach(() => cleanup());

  it("explains the frontend-first boundary and never requests secret values", () => {
    renderPage();

    expect(screen.getByRole("heading", { name: "MQTT Publisher" })).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent("BE-9.12");
    expect(screen.getByLabelText("Username secret reference")).toHaveValue("mqtt.username");
    expect(screen.getByLabelText("Password secret reference")).toHaveValue("mqtt.password");
    expect(screen.queryByLabelText(/^Username$/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^Password$/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Test connection" })).toBeDisabled();
  });

  it("requires acknowledgement when the operator selects plain MQTT", () => {
    renderPage();
    fireEvent.change(screen.getByLabelText(/^Broker URL/), { target: { value: "mqtt://broker.example.com:1883" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("alert")).toHaveTextContent("Acknowledge the plain MQTT exposure");
    fireEvent.click(screen.getByRole("checkbox", { name: /I understand this connection is not encrypted/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("heading", { name: "Payload source aliases" })).toBeInTheDocument();
  });

  it("maps source aliases into a custom payload and previews unavailable data", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.change(screen.getByLabelText("Alias for Active power"), { target: { value: "meter_power" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("heading", { name: "Custom JSON payload" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("meter_power")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Generate JSON" }));
    expect((screen.getByLabelText(/^Advanced JSON template/) as HTMLTextAreaElement).value).toContain('{{value "meter_power"}}');
    fireEvent.click(screen.getByRole("tab", { name: "Unavailable" }));
    expect(screen.getByText(/"active_power_kw": null/)).toBeInTheDocument();
  });

  it("keeps diagnostics generic and saves a reference-only local draft", () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Publisher name"), { target: { value: "Recovered MQTT draft" } });
    for (let index = 0; index < 3; index += 1) fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("heading", { name: "Diagnostics and review" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Generic diagnostic monitor" })).toBeInTheDocument();
    expect(screen.getByText("No broker message is fabricated in this frontend-first view.")).toBeInTheDocument();
    expect(screen.queryByText("processed")).not.toBeInTheDocument();

    const config = screen.getByRole("heading", { name: "Server configuration preview" }).closest<HTMLElement>("div.mqtt-review");
    expect(config).not.toBeNull();
    expect(within(config!).getByText(/"username":/)).toBeInTheDocument();
    expect(within(config!).getByText(/"name": "mqtt.username"/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save local draft" }));

    expect(screen.getByRole("status")).toHaveTextContent("Draft saved in this browser");
    expect(localStorage.getItem("iot-edge.mqtt-publisher-draft.v1")).toContain("mqtt.username");

    cleanup();
    renderPage();
    expect(screen.getByLabelText("Publisher name")).toHaveValue("Recovered MQTT draft");
  });
});
