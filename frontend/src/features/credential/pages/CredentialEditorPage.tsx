import axios from "axios";
import { ArrowLeft, Check, CircleAlert, FileKey2, LoaderCircle, Save, ShieldCheck, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { createCredential, deleteCredentialSecret, getCredential, putCredentialSecret, updateCredential } from "../../../services/credential.service";
import type { CredentialProfile, PutCredentialSecretRequest } from "../../../types/credential";
import type { CredentialSecretSlot } from "../../../types/publisher";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../publisher/pages/MQTTPublisherWizardPage.css";
import "../../vgateway/pages/VGatewayListPage.css";
import "./CredentialPage.css";

function bytesToBase64(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    return body?.error?.message ?? fallback;
  }
  return error instanceof Error ? error.message : fallback;
}

function displayTime(value?: string): string {
  if (!value) return "Never";
  return new Date(value).toLocaleString();
}

function CredentialEditorPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [profile, setProfile] = useState<CredentialProfile | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [customCA, setCustomCA] = useState("");
  const [clientCertificate, setClientCertificate] = useState("");
  const [clientPrivateKey, setClientPrivateKey] = useState("");
  const [state, setState] = useState<"loading" | "ready" | "error">(id ? "loading" : "ready");
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [notice, setNotice] = useState("");
  const secretBySlot = useMemo(() => new Map(profile?.secrets.map((secret) => [secret.slot, secret]) ?? []), [profile]);

  useEffect(() => {
    if (!id) return;
    const controller = new AbortController();
    void getCredential(id, controller.signal).then((loaded) => {
      if (controller.signal.aborted) return;
      setProfile(loaded);
      setName(loaded.name);
      setDescription(loaded.description ?? "");
      setState("ready");
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setState("error");
        setMessage(errorMessage(error, "Unable to load Credential Profile"));
      }
    });
    return () => controller.abort();
  }, [id]);

  async function saveProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) {
      setMessage("Profile name is required");
      return;
    }
    setBusy("profile");
    setMessage("");
    try {
      const saved = profile
        ? await updateCredential(profile.id, { name: name.trim(), description: description.trim() || null })
        : await createCredential({ type: "mqtt", name: name.trim(), description: description.trim() || null });
      setProfile(saved);
      setNotice(profile ? "Credential Profile updated." : "Profile created. Add only the secret material required by your broker.");
      if (!profile) navigate(`/credentials/${encodeURIComponent(saved.id)}`, { replace: true });
    } catch (error) {
      setMessage(errorMessage(error, "Unable to save Credential Profile"));
    } finally {
      setBusy("");
    }
  }

  async function upload(slot: CredentialSecretSlot, request: PutCredentialSecretRequest, clear: () => void) {
    if (!profile) return;
    setBusy(slot);
    setMessage("");
    try {
      await putCredentialSecret(profile.id, slot, request);
      clear();
      setProfile(await getCredential(profile.id));
      setNotice(`${slot} encrypted and stored. Its value cannot be read back.`);
    } catch (error) {
      setMessage(errorMessage(error, "Unable to store Credential secret"));
    } finally {
      setBusy("");
    }
  }

  async function remove(slot: CredentialSecretSlot) {
    if (!profile || !window.confirm(`Delete ${slot} from this profile?`)) return;
    setBusy(slot);
    try {
      await deleteCredentialSecret(profile.id, slot);
      setProfile(await getCredential(profile.id));
      setNotice(`${slot} deleted.`);
    } catch (error) {
      setMessage(errorMessage(error, "Unable to delete Credential secret"));
    } finally {
      setBusy("");
    }
  }

  function secretCard(slot: CredentialSecretSlot, label: string, value: string, setValue: (value: string) => void, passwordField = false) {
    const metadata = secretBySlot.get(slot);
    return <article className="credential-secret-card"><header><div><strong>{label}</strong><small>{metadata ? `Stored · revision ${metadata.revision}` : "Optional · not stored"}</small></div>{metadata && <Check size={17} />}</header><label><span>{metadata ? `Rotate ${label}` : label}</span><input type={passwordField ? "password" : "text"} autoComplete="off" value={value} onChange={(event) => setValue(event.target.value)} /></label><footer><small>{metadata ? `Last rotated ${displayTime(metadata.rotated_at)}` : "Leave blank when the broker does not require it."}</small><div>{metadata && <button type="button" className="is-danger" disabled={Boolean(busy)} onClick={() => void remove(slot)}><Trash2 size={15} />Delete</button>}<button type="button" disabled={!value || Boolean(busy)} onClick={() => void upload(slot, { value_base64: bytesToBase64(value) }, () => setValue(""))}><FileKey2 size={15} />{metadata ? "Rotate" : "Store"}</button></div></footer></article>;
  }

  if (state === "loading") return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Credentials</>}><div className="mqtt-loading"><LoaderCircle className="is-spinning" />Loading Credential Profile…</div></VGatewayShell>;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Credentials <span>/</span> <strong>{profile?.name || "New"}</strong></>}>
    <div className="mqtt-wizard-content credential-editor">
      <header className="mqtt-heading"><Link to="/credentials"><ArrowLeft size={17} />Credentials</Link><div className="mqtt-heading-row"><div><p>Core encrypted vault</p><h1>{profile?.name || "New MQTT Credential Profile"}</h1><span>Reusable authentication and TLS material for one or more MQTT Publishers.</span></div><div className="mqtt-contract-badge"><ShieldCheck size={18} /><span><strong>Write-only secrets</strong><small>{profile ? `${profile.usage_count} Publisher link(s)` : "Create profile first"}</small></span></div></div></header>
      <div className="mqtt-api-note"><ShieldCheck size={18} /><span><strong>Optional by design.</strong> Store only what the broker requires. Username/password, custom CA, and mTLS identity can all remain empty.</span></div>
      <form className="mqtt-panel credential-profile-form" onSubmit={saveProfile}><div className="mqtt-panel-heading"><FileKey2 /><div><h2>Profile details</h2><p>Publishers reference this profile by ID; renaming it does not break connections.</p></div></div><div className="mqtt-form-grid"><label><span>Profile name</span><input autoFocus={!id} value={name} onChange={(event) => setName(event.target.value)} /></label><label><span>Description <i>optional</i></span><input value={description} onChange={(event) => setDescription(event.target.value)} /></label></div><div className="credential-save-row"><button className="mqtt-primary-button" disabled={Boolean(busy)} type="submit"><Save size={16} />{profile ? "Save profile" : "Create profile"}</button></div></form>
      {profile && <section className="mqtt-panel"><div className="mqtt-panel-heading"><ShieldCheck /><div><h2>Encrypted material</h2><p>Entering a new value rotates that slot. Existing values are never displayed.</p></div></div><div className="credential-secret-grid">{secretCard("mqtt.username", "Username", username, setUsername)}{secretCard("mqtt.password", "Password", password, setPassword, true)}<article className="credential-secret-card is-wide"><header><div><strong>Custom CA certificate</strong><small>{secretBySlot.has("mqtt.custom_ca") ? `Stored · revision ${secretBySlot.get("mqtt.custom_ca")?.revision}` : "Optional · system roots are used by default"}</small></div>{secretBySlot.has("mqtt.custom_ca") && <Check size={17} />}</header><label><span>CA certificate PEM</span><textarea spellCheck={false} value={customCA} onChange={(event) => setCustomCA(event.target.value)} /></label><footer><small>{secretBySlot.has("mqtt.custom_ca") ? `Last rotated ${displayTime(secretBySlot.get("mqtt.custom_ca")?.rotated_at)}` : "Use only when the broker certificate chains to a private CA."}</small><div>{secretBySlot.has("mqtt.custom_ca") && <button type="button" className="is-danger" onClick={() => void remove("mqtt.custom_ca")}><Trash2 size={15} />Delete</button>}<button type="button" disabled={!customCA || Boolean(busy)} onClick={() => void upload("mqtt.custom_ca", { certificate_pem_base64: bytesToBase64(customCA) }, () => setCustomCA(""))}><FileKey2 size={15} />Store CA</button></div></footer></article><article className="credential-secret-card is-wide"><header><div><strong>mTLS client identity</strong><small>{secretBySlot.has("mqtt.client_identity") ? `Stored · revision ${secretBySlot.get("mqtt.client_identity")?.revision}` : "Optional · certificate and private key rotate together"}</small></div>{secretBySlot.has("mqtt.client_identity") && <Check size={17} />}</header><div className="credential-pem-grid"><label><span>Client certificate PEM</span><textarea spellCheck={false} value={clientCertificate} onChange={(event) => setClientCertificate(event.target.value)} /></label><label><span>Private key PEM</span><textarea spellCheck={false} value={clientPrivateKey} onChange={(event) => setClientPrivateKey(event.target.value)} /></label></div><footer><small>{secretBySlot.has("mqtt.client_identity") ? `Last rotated ${displayTime(secretBySlot.get("mqtt.client_identity")?.rotated_at)}` : "Required only when the broker enforces mutual TLS."}</small><div>{secretBySlot.has("mqtt.client_identity") && <button type="button" className="is-danger" onClick={() => void remove("mqtt.client_identity")}><Trash2 size={15} />Delete</button>}<button type="button" disabled={!clientCertificate || !clientPrivateKey || Boolean(busy)} onClick={() => void upload("mqtt.client_identity", { certificate_pem_base64: bytesToBase64(clientCertificate), private_key_pem_base64: bytesToBase64(clientPrivateKey) }, () => { setClientCertificate(""); setClientPrivateKey(""); })}><FileKey2 size={15} />Store identity</button></div></footer></article></div></section>}
      {message && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{message}</div>}
      {notice && <div className="mqtt-message" role="status"><Check size={18} />{notice}</div>}
      {state === "error" && <Link to="/credentials">Return to Credentials</Link>}
    </div>
  </VGatewayShell>;
}

export default CredentialEditorPage;
