import { useEffect, useState } from "react";
import { Activity, CircleAlert, Server } from "lucide-react";

import {
  getHealth,
  type HealthResponse,
} from "../../../services/health.service";

type HealthCheckState =
  | { kind: "loading" }
  | { kind: "connected"; health: HealthResponse }
  | { kind: "error" };

function SystemHealthPage() {
  const [healthState, setHealthState] = useState<HealthCheckState>({
    kind: "loading",
  });

  useEffect(() => {
    const controller = new AbortController();

    async function checkBackendHealth() {
      try {
        const health = await getHealth(controller.signal);

        if (!controller.signal.aborted) {
          setHealthState({ kind: "connected", health });
        }
      } catch {
        if (!controller.signal.aborted) {
          setHealthState({ kind: "error" });
        }
      }
    }

    void checkBackendHealth();

    return () => {
      controller.abort();
    };
  }, []);

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-950 px-6 py-12 text-slate-100">
      <section className="w-full max-w-lg rounded-2xl border border-slate-800 bg-slate-900 p-8 shadow-2xl shadow-black/30">
        <div className="mb-8 flex items-center gap-4">
          <div className="rounded-xl bg-cyan-500/10 p-3 text-cyan-400">
            <Activity aria-hidden="true" size={28} />
          </div>
          <div>
            <p className="text-sm font-medium uppercase tracking-[0.2em] text-cyan-400">
              IoT Edge
            </p>
            <h1 className="text-2xl font-semibold text-white">System health</h1>
          </div>
        </div>

        {healthState.kind === "loading" && (
          <div
            className="flex items-center gap-3 rounded-xl border border-slate-700 bg-slate-800/60 p-5"
            role="status"
          >
            <span className="h-3 w-3 animate-pulse rounded-full bg-amber-400" />
            <p className="text-slate-300">Checking backend connection…</p>
          </div>
        )}

        {healthState.kind === "connected" && (
          <div
            className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 p-5"
            role="status"
          >
            <div className="mb-5 flex items-center gap-3 text-emerald-400">
              <Server aria-hidden="true" size={22} />
              <p className="font-semibold">Backend connected</p>
            </div>

            <dl className="grid grid-cols-2 gap-4 text-sm">
              <div>
                <dt className="text-slate-400">Status</dt>
                <dd className="mt-1 font-medium text-white">
                  {healthState.health.status}
                </dd>
              </div>
              <div>
                <dt className="text-slate-400">Version</dt>
                <dd className="mt-1 font-medium text-white">
                  {healthState.health.version}
                </dd>
              </div>
            </dl>
          </div>
        )}

        {healthState.kind === "error" && (
          <div
            className="flex items-start gap-3 rounded-xl border border-red-500/30 bg-red-500/10 p-5 text-red-300"
            role="alert"
          >
            <CircleAlert
              aria-hidden="true"
              className="mt-0.5 shrink-0"
              size={22}
            />
            <div>
              <p className="font-semibold">Backend unavailable</p>
              <p className="mt-1 text-sm text-red-200/80">
                Check that the API server is running on the configured URL.
              </p>
            </div>
          </div>
        )}
      </section>
    </main>
  );
}

export default SystemHealthPage;
