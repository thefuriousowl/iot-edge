import axios from "axios";
import { CircleAlert, FileKey2, LoaderCircle, RadioTower, RefreshCw, Search, Server, Settings2, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { deleteCredential, listCredentials } from "../../../services/credential.service";
import type { CredentialProfile } from "../../../types/credential";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../plugin/pages/PluginListPage.css";
import "../../vgateway/pages/VGatewayListPage.css";
import "./CredentialPage.css";

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    return body?.error?.message ?? "Unable to load Credential Profiles";
  }
  return error instanceof Error ? error.message : "Unable to load Credential Profiles";
}

function CredentialListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const search = searchParams.get("search")?.trim() ?? "";
  const rawType = searchParams.get("type");
  const profileType = rawType === "mqtt" || rawType === "http" ? rawType : undefined;
  const [profiles, setProfiles] = useState<CredentialProfile[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    void listCredentials({ type: profileType, search: search || undefined }, controller.signal).then((data) => {
      if (controller.signal.aborted) return;
      setProfiles(data);
      setState("ready");
      setMessage("");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setState("error");
      setMessage(errorMessage(error));
    });
    return () => controller.abort();
  }, [profileType, refresh, search]);

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const next = String(new FormData(event.currentTarget).get("search") ?? "").trim();
    const params = new URLSearchParams(searchParams);
    if (next) params.set("search", next); else params.delete("search");
    setSearchParams(params, { replace: true });
  }

  async function remove(profile: CredentialProfile) {
    if (!window.confirm(`Delete Credential Profile “${profile.name}”?`)) return;
    try {
      await deleteCredential(profile.id);
      setRefresh((value) => value + 1);
    } catch (error) {
      setMessage(errorMessage(error));
    }
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Credentials</strong></>}>
    <div className="plugin-content credential-content">
      <section className="plugin-heading"><div><p className="plugin-eyebrow">Core security</p><h1>Credential Profiles</h1><p>Store reusable MQTT and HTTP authentication material in the encrypted vault. Secret values are never returned by the API.</p></div><div className="publisher-heading-actions"><Link className="plugin-primary-link" to="/credentials/new?type=http"><Server size={18} />New HTTP Profile</Link><Link className="plugin-primary-link is-secondary" to="/credentials/new?type=mqtt"><RadioTower size={18} />New MQTT Profile</Link></div></section>
      <section className="plugin-metrics" aria-label="Credential summary"><article><FileKey2 /><span>Total Profiles</span><strong>{profiles.length}</strong></article><article><FileKey2 /><span>Encrypted slots</span><strong>{profiles.reduce((total, profile) => total + profile.secrets.length, 0)}</strong></article><article><FileKey2 /><span>Publisher links</span><strong>{profiles.reduce((total, profile) => total + profile.usage_count, 0)}</strong></article></section>
      <section className="plugin-toolbar"><form role="search" onSubmit={submitSearch}><Search size={18} /><input name="search" type="search" key={search} defaultValue={search} placeholder="Search Credential Profiles…" /><button type="submit">Search</button></form><select aria-label="Credential transport" value={profileType ?? "all"} onChange={(event) => { const params = new URLSearchParams(searchParams); if (event.target.value === "all") params.delete("type"); else params.set("type", event.target.value); setSearchParams(params, { replace: true }); }}><option value="all">All transports</option><option value="http">HTTP</option><option value="mqtt">MQTT</option></select><button type="button" onClick={() => setRefresh((value) => value + 1)}><RefreshCw className={state === "loading" ? "is-spinning" : ""} size={17} />Refresh</button></section>
      {message && <div className="plugin-alert" role="alert"><CircleAlert size={18} />{message}</div>}
      <section className="plugin-list" aria-label="Credential Profiles">
        {state === "loading" && <div className="plugin-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Credential Profiles…</strong></div>}
        {state === "error" && <div className="plugin-state is-error"><CircleAlert /><strong>Couldn’t load Credentials</strong><button onClick={() => setRefresh((value) => value + 1)}>Try again</button></div>}
        {state === "ready" && profiles.length === 0 && <div className="plugin-state"><FileKey2 /><strong>No Credential Profiles</strong><span>Create one for authenticated HTTP or MQTT, or use an anonymous/plaintext Publisher where explicitly allowed.</span><Link to="/credentials/new?type=http">Create HTTP Profile</Link></div>}
        {state === "ready" && profiles.length > 0 && <div className="plugin-table-scroll"><table><thead><tr><th>Profile</th><th>Type</th><th>Encrypted material</th><th>Used by</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{profiles.map((profile) => <tr key={profile.id}><td><strong>{profile.name}</strong><small>{profile.description || "No description"}</small></td><td>{profile.type === "http" ? "HTTP" : "MQTT"}</td><td>{profile.secrets.length} of {profile.type === "http" ? 7 : 4} slots<small>Vault revision {profile.secret_revision}</small></td><td>{profile.usage_count} Publisher{profile.usage_count === 1 ? "" : "s"}</td><td><div className="plugin-row-actions"><Link to={`/credentials/${encodeURIComponent(profile.id)}`}><Settings2 size={16} />Manage</Link><button className="is-danger" disabled={profile.usage_count > 0} title={profile.usage_count > 0 ? "Remove this profile from its Publishers first" : undefined} onClick={() => void remove(profile)}><Trash2 size={16} />Delete</button></div></td></tr>)}</tbody></table></div>}
      </section>
    </div>
  </VGatewayShell>;
}

export default CredentialListPage;
