// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import TagWizardPage from "./TagWizardPage";

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

vi.mock("../components/ReadingTagForm", () => ({
  default: ({ onChangeType }: { onChangeType: () => void }) => (
    <section aria-label="Reading Tag configuration">
      <h1>Reading Tag configuration</h1>
      <p>Tag type selected</p>
      <button type="button" onClick={onChangeType}>Change type</button>
    </section>
  ),
}));

vi.mock("../components/ConstantTagForm", () => ({
  default: ({ onChangeType }: { onChangeType: () => void }) => (
    <section aria-label="Constant Tag configuration">
      <h1>Constant Tag configuration</h1>
      <button type="button" onClick={onChangeType}>Change type</button>
    </section>
  ),
}));

vi.mock("../components/CalculatedTagForm", () => ({
  default: ({ onChangeType }: { onChangeType: () => void }) => (
    <section aria-label="Calculated Tag configuration">
      <h1>Calculated Tag configuration</h1>
      <button type="button" onClick={onChangeType}>Change type</button>
    </section>
  ),
}));

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{location.pathname}{location.search}</span>;
}

function renderWizard(path = "/tags/new") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/tags" element={<h1>Tag list</h1>} />
        <Route path="/tags/new" element={<TagWizardPage />} />
        <Route path="/tags/new/:type" element={<TagWizardPage />} />
      </Routes>
      <LocationProbe />
    </MemoryRouter>,
  );
}

describe("TagWizardPage", () => {
  afterEach(cleanup);

  it("renders an accessible type selection with Continue disabled initially", () => {
    renderWizard();

    expect(screen.getByRole("heading", { name: "Choose a tag type" })).toBeInTheDocument();
    expect(screen.getByRole("radiogroup", { name: "Tag type" })).toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(3);
    expect(screen.getByRole("radio", { name: /Reading Tag/ })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByRole("radio", { name: /Constant Tag/ })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Calculated Tag/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("persists the selected type in the URL and dispatches to configuration", () => {
    renderWizard();

    fireEvent.click(screen.getByRole("radio", { name: /Reading Tag/ }));
    expect(screen.getByRole("radio", { name: /Reading Tag/ })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("location")).toHaveTextContent("/tags/new?type=reading");

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/tags/new/reading");
    expect(screen.getByRole("heading", { name: "Reading Tag configuration" })).toBeInTheDocument();
    expect(screen.getByText("Tag type selected")).toBeInTheDocument();
  });

  it("hydrates a valid URL selection and ignores an invalid query value", () => {
    const valid = renderWizard("/tags/new?type=constant");
    expect(screen.getByRole("radio", { name: /Constant Tag/ })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
    valid.unmount();

    renderWizard("/tags/new?type=unsupported");
    expect(screen.getAllByRole("radio").every((choice) => choice.getAttribute("aria-checked") === "false")).toBe(true);
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("dispatches Constant Tag selection to its configuration form", () => {
    renderWizard("/tags/new?type=constant");

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/tags/new/constant");
    expect(screen.getByRole("heading", { name: "Constant Tag configuration" })).toBeInTheDocument();
  });

  it("supports changing the dispatched type and canceling to the list", () => {
    renderWizard("/tags/new/calculated");

    expect(screen.getByRole("heading", { name: "Calculated Tag configuration" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Change type" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/tags/new?type=calculated");
    expect(screen.getByRole("radio", { name: /Calculated Tag/ })).toHaveAttribute("aria-checked", "true");

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("heading", { name: "Tag list" })).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/tags");
  });

  it("redirects unsupported configuration routes to type selection", () => {
    renderWizard("/tags/new/unsupported");

    expect(screen.getByRole("heading", { name: "Choose a tag type" })).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/tags/new");
  });
});
