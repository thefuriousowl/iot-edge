import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(fileURLToPath(new URL("..", import.meta.url)), "dist");
const port = Number.parseInt(process.env.IOT_EDGE_FIXTURE_PORT ?? "4173", 10);
let authMode = "authenticated";
const now = "2026-08-28T10:00:00Z";
const pagination = (total) => ({ page: 1, per_page: 500, total, total_pages: total ? 1 : 0 });
const asset = (id, name, position) => ({ id, parent_id: null, name, kind: "area", description: null, enabled: true, timezone: "Asia/Bangkok", position, metadata: {}, created_at: now, updated_at: now });
const assets = [asset("asset-energy", "Chiller Plant", 0), asset("asset-air", "Compressor Room", 1)];
const plugin = { id: "energy-1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1, config: { logger_id: "logger-1", electrical_power_tags: [{ tag_id: "power", unit: "kW" }], thermal_power_tags: [{ tag_id: "thermal", unit: "kW" }], timezone: "Asia/Bangkok", max_gap_seconds: 120, tariff: { mode: "flat", currency: "THB", rate_per_kwh: 4 } }, runtime: { state: "running", started_at: now, last_transition_at: now, error: null }, created_at: now, updated_at: now };
const energyMetric = (kilowatt_hours, coverage_percent = 100, skipped_seconds = 0, issues = null) => ({ kilowatt_hours, covered_seconds: 3600 - skipped_seconds, skipped_seconds, coverage_percent, segments: 60, skipped_segments: skipped_seconds ? 1 : 0, issues });
const period = (hour, electrical, thermal, coverage = 100) => ({ from: `2026-08-28T0${hour}:00:00Z`, to: `2026-08-28T0${hour + 1}:00:00Z`, electrical: energyMetric(electrical, coverage, coverage < 100 ? 600 : 0, coverage < 100 ? [{ from: `2026-08-28T0${hour}:10:00Z`, to: `2026-08-28T0${hour}:20:00Z`, code: "missing_sample", errors: [{ code: "missing_sample", tag_id: "power", at: `2026-08-28T0${hour}:10:00Z`, message: "Fixture sample gap" }] }] : null), thermal: energyMetric(thermal, coverage), cost: { value: electrical * 4, valid: coverage === 100, error: coverage === 100 ? undefined : "tariff coverage incomplete" }, cop: { value: thermal / electrical, valid: coverage === 100, error: coverage === 100 ? undefined : "thermal coverage incomplete" } });
const historyRows = [period(4, 18, 54), period(3, 15, 45, 83.3), period(2, 12, 36), period(1, 10, 30)];
const summary = (electrical, thermal, coverage = 95) => ({ from: "2026-08-28T00:00:00Z", to: now, electrical: energyMetric(electrical, coverage, 600), thermal: energyMetric(thermal, coverage, 600), cost: { value: electrical * 4, valid: true }, cop: { value: thermal / electrical, valid: true } });
const overview = { instance_id: plugin.id, logger_id: "logger-1", timezone: "Asia/Bangkok", currency: "THB", tariff_mode: "flat", rate_per_kwh: 4, as_of: now, run: { id: "11111111-1111-4111-8111-111111111111", plugin_instance_id: plugin.id, name: "Chiller Plant", reason: "", status: "active", started_at: "2026-08-28T00:00:00Z", config_version: 1, created_at: "2026-08-28T00:00:00Z" }, latest: { batch_at: now, electrical: { kilowatts: 55, valid: true, errors: null }, thermal: { kilowatts: 145, valid: true, errors: null }, tariff: { rate_per_kwh: 4, valid: true, errors: null }, cop: { value: 2.64, valid: true } }, today: summary(420, 1100), month: summary(9200, 24500, 92) };
const energyArchives = [];
const logger = { id: "logger-1", name: "Plant history", description: null, enabled: true, timezone: "Asia/Bangkok", mode: "interval", start_at: now, end_at: null, max_size_bytes: null, config: { interval_seconds: 60 }, tag_count: 2, tags: [{ id: "power", name: "ActivePowerTotal_kW", type: "reading", data_type: "float64", enabled: true }, { id: "thermal", name: "ThermalEnergy_kW", type: "calculated", data_type: "float64", enabled: true }], created_at: now, updated_at: now };
const report = { id: "report-1", name: "Plant energy report", description: "Persisted Logger energy", logger_id: logger.id, logger_name: logger.name, timezone: logger.timezone, mode: "aggregate", bucket: "1h", column_count: 2, created_at: now, updated_at: now };
const publisher = { id: "publisher-1", type: "http_server", name: "Plant snapshot", enabled: true, config_version: 1, source_count: 2, runtime: { state: "running", last_transition_at: now, request_count: 4, publish_count: 10, failure_count: 0, queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: true, connection_count: 1, delivery_count: 10, delivery_failure_count: 0, transport_drop_count: 0, external_request_count: 4, rejected_request_count: 0, active_connections: 0 }, created_at: now, updated_at: now };

function output(key, value, quantity, unit, quality = "good") {
  const source = { kind: "plugin_output", plugin_instance_id: "compressed-air-1", output_key: key };
  const semantic = { resource: "compressed_air", quantity, unit, precision: 3 };
  return { binding: { id: key, owner_asset_id: "asset-air", boundary_asset_id: "asset-air", source_key: `plugin_output:compressed-air-1:${key}`, source, semantic, meter_role: "direct", rollup_policy: "include" }, latest: { binding_id: key, source, semantic, available: value !== null, value, quality, observed_at: now, emitted_at: now, period_start: "2026-08-28T00:00:00Z", period_end: now, error: value === null ? "Fixture output unavailable" : undefined }, history: [], history_retention: "latest_only" };
}
const compressedMeasurements = [
  output("compressed_air.live_power", 80, "power", "kW"), output("compressed_air.live_flow", 400, "flow_rate", "Nm3/h"), output("compressed_air.live_pressure", 7, "pressure", "bar"), output("compressed_air.energy", 800, "energy", "kWh"), output("compressed_air.volume", 4000, "volume", "Nm3"), output("compressed_air.sec", 0.2, "ratio", "1"), output("compressed_air.cost", 3200, "cost", "1"), output("compressed_air.cost_per_volume", 0.8, "ratio", "1"), output("compressed_air.runtime_ratio", 0.9, "ratio", "1"), output("compressed_air.load_ratio", 0.75, "ratio", "1"), output("compressed_air.pressure_average", 6.8, "pressure", "bar"), output("compressed_air.pressure_stddev", 0.2, "pressure", "bar"), output("compressed_air.pressure_drop", 0.5, "pressure", "bar"), output("compressed_air.estimated_leak_flow", 40, "flow_rate", "Nm3/h"), output("compressed_air.estimated_leak_energy", 80, "energy", "kWh"), output("compressed_air.estimated_leak_cost", 320, "cost", "1"),
];
const tagMeasurement = (id, available, quality, observed_at, error) => { const source = { kind: "tag", tag_id: id }; const semantic = { resource: "electricity", quantity: "power", unit: "kW", precision: 2 }; return { binding: { id, owner_asset_id: "asset-energy", boundary_asset_id: "asset-energy", source_key: `tag:${id}`, source, semantic, meter_role: "direct", rollup_policy: "include" }, latest: { binding_id: id, source, semantic, available, value: available ? 10 : null, quality, observed_at, emitted_at: now, error }, history: [], history_retention: "runtime_memory" }; };
const qualityMeasurements = [tagMeasurement("missing", false, "bad", undefined, "No sample"), tagMeasurement("modbus", false, "bad", undefined, "Modbus timeout"), tagMeasurement("stale", true, "good", "2026-08-28T09:50:00Z"), tagMeasurement("good", true, "good", "2026-08-28T09:59:00Z")];

function json(response, value, status = 200) { response.writeHead(status, { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store" }); response.end(JSON.stringify(value)); }
function list(data, perPage = 100) { return { data, pagination: { page: 1, per_page: perPage, total: data.length, total_pages: data.length ? 1 : 0 } }; }
function api(request, response, url) {
  if (url.pathname === "/api/acceptance/auth-mode" && request.method === "POST") {
    const requested = url.searchParams.get("mode");
    if (!["authenticated", "login", "setup"].includes(requested)) return json(response, { error: { code: "INVALID_MODE", message: "Unknown acceptance auth mode" } }, 400);
    authMode = requested;
    return json(response, { mode: authMode });
  }
  if (url.pathname === "/api/auth/setup/status") return json(response, { setup_required: authMode === "setup" });
  if (url.pathname === "/api/auth/refresh") return authMode === "authenticated" ? json(response, { access_token: "fixture-access-token", expires_in: 3600 }) : json(response, { error: { code: "UNAUTHORIZED", message: "Acceptance session unavailable" } }, 401);
  if (url.pathname === "/api/auth/me") return json(response, { id: "fixture-user", username: "acceptance", created_at: now, last_login: now });
  if (url.pathname === "/api/health") return json(response, { status: "ok", version: "fixture" });
  if (url.pathname === "/api/system/internet-status") return json(response, { status: "online", checked_at: now, latency_ms: 1 });
  if (url.pathname === "/api/sse/tags") { response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" }); response.write(": fixture connected\n\n"); return; }
  if (url.pathname === "/api/assets/roots") return json(response, { data: assets });
  if (url.pathname === "/api/assets") return json(response, list(assets));
  if (url.pathname === "/api/assets/asset-air/measurements") return json(response, { asset_id: "asset-air", captured_at: now, measurements: compressedMeasurements });
  if (url.pathname === "/api/assets/asset-energy/measurements") return json(response, { asset_id: "asset-energy", captured_at: now, measurements: qualityMeasurements });
  if (url.pathname.endsWith("/bindings")) return json(response, { data: [{ id: "energy-binding", owner_asset_id: "asset-energy", boundary_asset_id: "asset-energy", source_key: "plugin_output:energy-1:today.electrical_energy_kwh", source: { kind: "plugin_output", plugin_instance_id: "energy-1", output_key: "today.electrical_energy_kwh" }, semantic: { resource: "electricity", quantity: "energy", unit: "kWh", precision: 2 }, meter_role: "direct", rollup_policy: "include" }] });
  if (url.pathname === "/api/plugins") return json(response, list([{ ...plugin, config: undefined }]));
  if (url.pathname === "/api/plugins/energy-1") return json(response, plugin);
  if (url.pathname === "/api/plugins/energy-1/energy/overview") return json(response, overview);
  if (url.pathname === "/api/plugins/energy-1/energy/reset" && request.method === "POST") {
    energyArchives.unshift({ ...overview.run, status: "archived", ended_at: now, archived_at: now, archive_sha256: "a".repeat(64) });
    overview.run = { ...overview.run, id: "22222222-2222-4222-8222-222222222222", name: "Chiller 2 inlet", reason: "Moved by operator", started_at: now, created_at: now };
    overview.today = summary(0, 0, 0); overview.month = summary(0, 0, 0);
    return json(response, overview.run);
  }
  if (url.pathname === "/api/plugins/energy-1/energy/archives") return json(response, { data: energyArchives });
  if (/^\/api\/plugins\/energy-1\/energy\/archives\/[^/]+\/export\.csv$/.test(url.pathname)) { response.writeHead(200, { "Content-Type": "text/csv; charset=utf-8" }); return response.end("run_id,run_name,from,to,electrical_kwh\n11111111-1111-4111-8111-111111111111,Chiller Plant,2026-08-28T07:00:00+07:00,2026-08-28T17:00:00+07:00,420\n"); }
  if (url.pathname === "/api/plugins/energy-1/energy/stream") { response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" }); response.write(`retry: 3000\n\nid: fixture:1\nevent: energy_metrics\ndata: ${JSON.stringify({ id: "fixture:1", sequence: 1, instance_id: plugin.id, metrics: { batch_at: "2026-08-28T10:01:00Z", electrical: { kilowatts: 66, valid: true, errors: null }, thermal: { kilowatts: 165, valid: true, errors: null }, tariff: { rate_per_kwh: 4, valid: true, errors: null }, cop: { value: 2.5, valid: true } } })}\n\n`); return; }
  if (url.pathname === "/api/plugins/energy-1/energy/history") { const requestedBucket = url.searchParams.get("bucket") ?? "1h"; return json(response, { instance_id: plugin.id, logger_id: "logger-1", timezone: "Asia/Bangkok", bucket: requestedBucket, requested_bucket: requestedBucket, downsampled: false, point_limit: 500, currency: "THB", tariff_mode: "flat", rate_per_kwh: 4, data: historyRows, pagination: pagination(historyRows.length) }); }
  if (url.pathname === "/api/vgateways") return json(response, list([{ id: "gateway-1", name: "Plant gateway", type: "modbus_tcp", description: null, enabled: true, status: "connected", device_count: 2, created_at: now, updated_at: now }]));
  if (url.pathname === "/api/devices") return json(response, list([]));
  if (url.pathname === "/api/tags") return json(response, list([]));
  if (url.pathname === "/api/data-loggers") return json(response, list([logger]));
  if (url.pathname === "/api/data-loggers/logger-1") return json(response, logger);
  if (url.pathname === "/api/reports") return json(response, list([report], 20));
  if (url.pathname === "/api/data-publishers") return json(response, list([publisher], 20));
  return json(response, { error: { code: "FIXTURE_NOT_FOUND", message: `No fixture for ${url.pathname}` } }, 404);
}

const mime = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".woff2": "font/woff2" };
const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://${request.headers.host ?? "127.0.0.1"}`);
  if (url.pathname.startsWith("/api/")) return api(request, response, url);
  let path = normalize(join(root, decodeURIComponent(url.pathname)));
  if (!path.startsWith(root)) { response.writeHead(403); return response.end(); }
  try { if ((await stat(path)).isDirectory()) path = url.pathname === "/" ? join(path, "index.html") : join(root, "index.html"); } catch { path = join(root, "index.html"); }
  try { const info = await stat(path); response.writeHead(200, { "Content-Type": mime[extname(path)] ?? "application/octet-stream", "Content-Length": info.size, "Cache-Control": "no-store" }); createReadStream(path).pipe(response); } catch { response.writeHead(404); response.end("fixture asset not found"); }
});
server.listen(port, "127.0.0.1", () => process.stdout.write(`IoT Edge dashboard fixture server listening on http://127.0.0.1:${port}\n`));
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.close(() => process.exit(0)));
