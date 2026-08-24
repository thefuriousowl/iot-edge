import axe from "axe-core";
import { expect } from "vitest";

export async function expectNoAxeViolations(container: HTMLElement): Promise<void> {
  const results = await axe.run(container, {
    rules: {
      "color-contrast": { enabled: false },
    },
  });
  const details = results.violations.map((violation) =>
    `${violation.id}: ${violation.help}\n${violation.nodes.map((node) => `  ${node.target.join(" ")}: ${node.failureSummary ?? ""}`).join("\n")}`,
  ).join("\n\n");

  expect(results.violations, details).toEqual([]);
}
