import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chromium } from "playwright-core";

const port = 42000 + Math.floor(Math.random() * 1000);
const baseURL = `http://127.0.0.1:${port}`;
const server = spawn(process.execPath, ["acceptance/dashboard-fixture-server.mjs"], {
  cwd: new URL("..", import.meta.url),
  env: { ...process.env, IOT_EDGE_FIXTURE_PORT: String(port) },
  stdio: ["ignore", "pipe", "pipe"],
});

let serverError = "";
server.stderr.setEncoding("utf8");
server.stderr.on("data", (chunk) => { serverError += chunk; });

async function waitForServer() {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    if (server.exitCode !== null) throw new Error(`Fixture server exited early: ${serverError}`);
    try {
      const response = await fetch(`${baseURL}/api/health`);
      if (response.ok) return;
    } catch { /* server is still starting */ }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Fixture server did not become ready");
}

async function setAuthMode(mode) {
  const response = await fetch(`${baseURL}/api/acceptance/auth-mode?mode=${mode}`, { method: "POST" });
  assert.equal(response.ok, true, `Unable to select ${mode} auth mode`);
}

async function expectHeading(page, path, name) {
  await page.goto(`${baseURL}${path}`);
  try {
    await page.getByRole("heading", { name, exact: true }).waitFor({ timeout: 10_000 });
  } catch (error) {
    const body = (await page.locator("body").innerText()).slice(0, 1000);
    throw new Error(`${path} did not render heading ${JSON.stringify(name)} at ${page.url()}:\n${body}`, { cause: error });
  }
  assert.equal(await page.locator('[role="alert"]').count(), 0, `${path} rendered an alert`);
}

let browser;
try {
  await waitForServer();
  browser = await chromium.launch({ channel: "msedge", headless: true });

  await setAuthMode("setup");
  let context = await browser.newContext();
  let page = await context.newPage();
  await expectHeading(page, "/", "Set up your account");
  await context.close();

  await setAuthMode("login");
  context = await browser.newContext();
  page = await context.newPage();
  await expectHeading(page, "/", "Welcome back");
  await context.close();

  await setAuthMode("authenticated");
  context = await browser.newContext();
  page = await context.newPage();
  const consoleErrors = [];
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("pageerror", (error) => consoleErrors.push(error.message));

  await expectHeading(page, "/vgateways", "vGateways");
  await page.getByText("Plant gateway", { exact: true }).waitFor();
  await expectHeading(page, "/assets", "Asset Explorer");
  await page.getByText("Chiller Plant", { exact: true }).first().waitFor();
  await expectHeading(page, "/data-loggers", "Data Loggers");
  await page.getByText("Plant history", { exact: true }).waitFor();
  await expectHeading(page, "/plugins/energy-1/energy", "Plant Energy");
  await page.getByText("66.00 kW", { exact: true }).waitFor();
  await page.getByText("420.00 kWh", { exact: true }).waitFor();
  await expectHeading(page, "/utilities/compressed-air?asset=asset-air", "Compressed Air Efficiency");
  await page.getByText("Time comparison unavailable", { exact: true }).waitFor();
  await expectHeading(page, "/utilities/data-quality?asset=asset-energy", "Data Quality & Coverage");
  await expectHeading(page, "/reports", "Reports");
  await page.getByText("Plant energy report", { exact: true }).waitFor();
  await expectHeading(page, "/data-publishers", "Data Publishers");
  await page.getByText("Plant snapshot", { exact: true }).waitFor();

  assert.deepEqual(consoleErrors, [], `Browser console errors:\n${consoleErrors.join("\n")}`);
  await context.close();
  process.stdout.write("Critical browser E2E passed: setup, login, acquisition, Assets, Logger, utilities, dashboards, Reports, Plugin outputs, and Publishers.\n");
} finally {
  await browser?.close();
  if (server.exitCode === null) {
    server.kill("SIGTERM");
    await Promise.race([once(server, "exit"), new Promise((resolve) => setTimeout(resolve, 3000))]);
  }
}
