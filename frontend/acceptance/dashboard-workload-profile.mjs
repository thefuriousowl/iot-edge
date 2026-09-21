import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chromium } from "playwright-core";

const clientCount = 10;
const port = 43000 + Math.floor(Math.random() * 1000);
const baseURL = `http://127.0.0.1:${port}`;
const server = spawn(process.execPath, ["acceptance/dashboard-fixture-server.mjs"], {
  cwd: new URL("..", import.meta.url), env: { ...process.env, IOT_EDGE_FIXTURE_PORT: String(port) }, stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (server.exitCode !== null) throw new Error("Dashboard fixture exited before profiling");
    try { if ((await fetch(`${baseURL}/api/health`)).ok) return; } catch { /* starting */ }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Dashboard fixture did not become ready");
}

const percentile = (values, fraction) => values[Math.min(values.length - 1, Math.ceil(values.length * fraction) - 1)];
let browser;
try {
  await waitForServer();
  browser = await chromium.launch({ channel: "msedge", headless: true });
  const consoleErrors = [];
  const contexts = await Promise.all(Array.from({ length: clientCount }, () => browser.newContext({ acceptDownloads: true })));
  const pages = await Promise.all(contexts.map((context) => context.newPage()));
  for (const page of pages) {
    page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
    page.on("pageerror", (error) => consoleErrors.push(error.message));
  }
  const durations = await Promise.all(pages.map(async (page) => {
    const started = performance.now();
    await page.goto(`${baseURL}/plugins/energy-1/energy`);
    try { await page.getByRole("heading", { name: "Plant Energy", exact: true }).waitFor(); }
    catch (error) { throw new Error(`dashboard failed at ${page.url()}: ${(await page.locator("body").innerText()).slice(0, 500)}`, { cause: error }); }
    await page.getByText("66.00 kW", { exact: true }).waitFor();
    return performance.now() - started;
  }));
  durations.sort((left, right) => left - right);

  const exportPage = pages[0];
  await exportPage.goto(`${baseURL}/plugins/energy-1/energy/history?range=1h&bucket=15m`);
  await exportPage.getByText("Showing 4 of 4 loaded Plugin buckets", { exact: false }).waitFor();
  const exportStarted = performance.now();
  const downloadPromise = exportPage.waitForEvent("download");
  await exportPage.getByRole("button", { name: "Export CSV" }).click();
  const download = await downloadPromise;
  const exportMilliseconds = performance.now() - exportStarted;
  assert.match(download.suggestedFilename(), /\.csv$/i);
  assert.deepEqual(consoleErrors, []);

  process.stdout.write(`WORKLOAD_BROWSER ${JSON.stringify({
    clients: clientCount,
    dashboard_load_p50_ms: Math.round(percentile(durations, 0.5)),
    dashboard_load_p95_ms: Math.round(percentile(durations, 0.95)),
    dashboard_load_max_ms: Math.round(durations.at(-1)),
    csv_export_ms: Math.round(exportMilliseconds),
  })}\n`);
  await Promise.all(contexts.map((context) => context.close()));
} finally {
  await browser?.close();
  if (server.exitCode === null) {
    server.kill("SIGTERM");
    await Promise.race([once(server, "exit"), new Promise((resolve) => setTimeout(resolve, 3000))]);
  }
}
