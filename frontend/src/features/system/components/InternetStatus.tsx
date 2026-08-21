import { useEffect, useState } from "react";
import { CircleHelp, LoaderCircle, Wifi, WifiOff } from "lucide-react";

import {
  getInternetStatus,
  type InternetStatusResponse,
} from "../../../services/health.service";
import "./InternetStatus.css";

type InternetState =
  | { kind: "checking" }
  | { kind: "online"; result: InternetStatusResponse }
  | { kind: "offline"; result?: InternetStatusResponse }
  | { kind: "unknown" };

const checkIntervalMilliseconds = 30_000;

function InternetStatus() {
  const [state, setState] = useState<InternetState>({ kind: "checking" });

  useEffect(() => {
    let disposed = false;
    let activeController: AbortController | null = null;

    async function check(showChecking: boolean) {
      activeController?.abort();
      const controller = new AbortController();
      activeController = controller;
      if (showChecking) {
        setState({ kind: "checking" });
      }

      try {
        const result = await getInternetStatus(controller.signal);
        if (!disposed && !controller.signal.aborted) {
          setState({ kind: result.status, result });
        }
      } catch {
        if (!disposed && !controller.signal.aborted) {
          setState({ kind: "unknown" });
        }
      }
    }

    function handleOffline() {
      activeController?.abort();
      setState({ kind: "offline" });
    }

    function handleOnline() {
      void check(true);
    }

    void check(true);
    const interval = window.setInterval(
      () => void check(false),
      checkIntervalMilliseconds,
    );
    window.addEventListener("offline", handleOffline);
    window.addEventListener("online", handleOnline);

    return () => {
      disposed = true;
      activeController?.abort();
      window.clearInterval(interval);
      window.removeEventListener("offline", handleOffline);
      window.removeEventListener("online", handleOnline);
    };
  }, []);

  const label =
    state.kind === "checking"
      ? "Checking"
      : state.kind === "online"
        ? "Online"
        : state.kind === "offline"
          ? "Offline"
          : "Unknown";
  const title =
    state.kind === "online"
      ? `Internet online${state.result.latency_ms === null ? "" : ` · ${state.result.latency_ms.toFixed(2)} ms`}`
      : state.kind === "offline"
        ? "Internet connection unavailable"
        : state.kind === "unknown"
          ? "Unable to verify internet connection"
          : "Checking internet connection";

  return (
    <div
      className={`internet-status is-${state.kind}`}
      role="status"
      aria-label={`Internet connection: ${label}`}
      title={title}
    >
      {state.kind === "checking" && <LoaderCircle aria-hidden="true" className="is-spinning" size={18} />}
      {state.kind === "online" && <Wifi aria-hidden="true" size={18} />}
      {state.kind === "offline" && <WifiOff aria-hidden="true" size={18} />}
      {state.kind === "unknown" && <CircleHelp aria-hidden="true" size={18} />}
      <span>
        <small>Internet</small>
        <strong>{label}</strong>
      </span>
    </div>
  );
}

export default InternetStatus;
