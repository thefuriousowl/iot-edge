import { LoaderCircle } from "lucide-react";

function SessionLoading() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-950 px-6 text-slate-100">
      <div
        className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 px-5 py-4 shadow-2xl shadow-black/30"
        role="status"
      >
        <LoaderCircle
          aria-hidden="true"
          className="animate-spin text-cyan-400"
          size={22}
        />
        <span className="text-slate-300">Checking your session…</span>
      </div>
    </main>
  );
}

export default SessionLoading;
