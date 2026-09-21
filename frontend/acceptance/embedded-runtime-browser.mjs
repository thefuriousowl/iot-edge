import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const baseURL = process.env.IOT_EDGE_BASE_URL;
assert.match(baseURL ?? "", /^http:\/\/127\.0\.0\.1:\d+$/, "IOT_EDGE_BASE_URL must be a loopback URL");

const browser = await chromium.launch({ channel: "msedge", headless: true });
try {
  const page = await browser.newPage();
  const errors = [];
  page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()); });
  page.on("pageerror", (error) => errors.push(error.message));
  const response = await page.goto(`${baseURL}/login`);
  assert.equal(response?.status(), 200);
  await page.getByRole("heading", { name: "Set up your account", exact: true }).waitFor({ timeout: 10_000 });
  assert.equal(await page.locator('[role="alert"]').count(), 0);
  assert.deepEqual(errors, []);
  process.stdout.write("Embedded runtime Edge smoke passed: deep-link SPA, same-origin API and clean console.\n");
} finally {
  await browser.close();
}
