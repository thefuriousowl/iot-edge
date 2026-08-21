import { useEffect, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import axios from "axios";
import {
  CheckCircle2,
  CircleAlert,
  LoaderCircle,
  Radio,
  Save,
} from "lucide-react";
import { useForm } from "react-hook-form";
import { useNavigate, useParams } from "react-router-dom";
import { z } from "zod";

import {
  createVGateway,
  getVGateway,
  testVGatewayConnection,
  updateVGateway,
} from "../../../services/vgateway.service";
import type { TestVGatewayConnectionResponse } from "../../../types/vgateway";
import VGatewayShell from "../components/VGatewayShell";
import "./VGatewayListPage.css";
import "./VGatewayFormPage.css";

const formSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "Gateway name is required")
    .max(100, "Gateway name must not exceed 100 characters"),
  description: z
    .string()
    .max(1000, "Description must not exceed 1000 characters"),
  enabled: z.boolean(),
  host: z.string().trim().min(1, "Host is required"),
  port: z
    .number()
    .int("Port must be a whole number")
    .min(1, "Port must be between 1 and 65535")
    .max(65535, "Port must be between 1 and 65535"),
  timeout: z
    .number()
    .int("Timeout must be a whole number")
    .min(100, "Timeout must be between 100 and 60000 ms")
    .max(60000, "Timeout must be between 100 and 60000 ms"),
  retry_count: z
    .number()
    .int("Retry count must be a whole number")
    .min(0, "Retry count must be between 0 and 10")
    .max(10, "Retry count must be between 0 and 10"),
  retry_delay: z
    .number()
    .int("Retry delay must be a whole number")
    .min(0, "Retry delay must not be negative"),
  keep_alive: z.boolean(),
  reconnect_interval: z
    .number()
    .int("Reconnect interval must be a whole number")
    .min(0, "Reconnect interval must not be negative"),
});

type FormValues = z.infer<typeof formSchema>;
type PageState = "ready" | "loading" | "error";

const defaultValues: FormValues = {
  name: "",
  description: "",
  enabled: true,
  host: "",
  port: 502,
  timeout: 5000,
  retry_count: 3,
  retry_delay: 1000,
  keep_alive: true,
  reconnect_interval: 30,
};

function apiErrorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as
      | { error?: { message?: string } }
      | undefined;
    if (data?.error?.message) {
      return data.error.message;
    }
  }
  return fallback;
}

function VGatewayFormPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const isEdit = Boolean(id);
  const [pageState, setPageState] = useState<PageState>(
    isEdit ? "loading" : "ready",
  );
  const [pageError, setPageError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [testResult, setTestResult] =
    useState<TestVGatewayConnectionResponse | null>(null);
  const [testing, setTesting] = useState(false);
  const [unitID, setUnitID] = useState("");
  const {
    formState: { errors, isSubmitting },
    handleSubmit,
    register,
    reset,
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues,
  });

  useEffect(() => {
    if (!id) {
      return;
    }

    const gatewayID = id;

    const controller = new AbortController();
    async function loadGateway() {
      setPageState("loading");
      setPageError(null);
      try {
        const gateway = await getVGateway(gatewayID, controller.signal);
        if (controller.signal.aborted) {
          return;
        }
        reset({
          name: gateway.name,
          description: gateway.description ?? "",
          enabled: gateway.enabled,
          host: gateway.config.host,
          port: gateway.config.port,
          timeout: gateway.config.timeout,
          retry_count: gateway.config.retry_count,
          retry_delay: gateway.config.retry_delay,
          keep_alive: gateway.config.keep_alive,
          reconnect_interval: gateway.config.reconnect_interval,
        });
        setPageState("ready");
      } catch (error) {
        if (!controller.signal.aborted) {
          setPageError(apiErrorMessage(error, "Unable to load this vGateway."));
          setPageState("error");
        }
      }
    }
    void loadGateway();
    return () => controller.abort();
  }, [id, reset]);

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null);
    const payload = {
      name: values.name.trim(),
      type: "modbus_tcp" as const,
      description: values.description.trim() || null,
      enabled: values.enabled,
      config: {
        host: values.host.trim(),
        port: values.port,
        timeout: values.timeout,
        retry_count: values.retry_count,
        retry_delay: values.retry_delay,
        keep_alive: values.keep_alive,
        reconnect_interval: values.reconnect_interval,
      },
    };

    try {
      if (id) {
        await updateVGateway(id, payload);
      } else {
        await createVGateway(payload);
      }
      navigate("/vgateways", { replace: true });
    } catch (error) {
      setSubmitError(apiErrorMessage(error, `Unable to ${isEdit ? "update" : "create"} the vGateway.`));
    }
  });

  async function handleConnectionTest() {
    if (!id) {
      return;
    }
    const parsedUnitID = unitID === "" ? undefined : Number(unitID);
    if (parsedUnitID !== undefined && (!Number.isInteger(parsedUnitID) || parsedUnitID < 0 || parsedUnitID > 255)) {
      setTestResult({ success: false, error: "Invalid unit ID", message: "Unit ID must be between 0 and 255." });
      return;
    }

    setTesting(true);
    setTestResult(null);
    try {
      const result = await testVGatewayConnection(id, parsedUnitID === undefined ? undefined : { unit_id: parsedUnitID });
      setTestResult(result);
    } catch (error) {
      setTestResult({
        success: false,
        error: "Connection test failed",
        message: apiErrorMessage(error, "Unable to test the vGateway connection."),
      });
    } finally {
      setTesting(false);
    }
  }

  const heading = isEdit ? "Edit vGateway" : "Add vGateway";

  return (
    <VGatewayShell breadcrumb={<><span>vGateways</span> <span>/</span> <strong>{isEdit ? "Edit Gateway" : "Add Gateway"}</strong></>}>
      <div className="vgateway-form-content">
        <header className="vgateway-form-heading">
          <h1>{heading}</h1>
          <p>{isEdit ? "Update gateway settings and verify connectivity." : "Configure a virtual gateway for your edge network."}</p>
        </header>

        {pageState === "loading" && (
          <div className="vgateway-form-state" role="status">
            <LoaderCircle aria-hidden="true" className="is-spinning" />
            Loading vGateway…
          </div>
        )}

        {pageState === "error" && (
          <div className="vgateway-form-state is-error" role="alert">
            <CircleAlert aria-hidden="true" />
            <strong>Couldn’t load vGateway</strong>
            <span>{pageError}</span>
            <button type="button" onClick={() => navigate("/vgateways")}>Back to vGateways</button>
          </div>
        )}

        {pageState === "ready" && (
          <form className="vgateway-form" onSubmit={onSubmit} noValidate>
            {submitError && (
              <div className="vgateway-form-alert" role="alert">
                <CircleAlert aria-hidden="true" size={19} />
                {submitError}
              </div>
            )}

            <section className="vgateway-form-section">
              <h2>General information</h2>
              <div className="vgateway-form-grid is-two-column">
                <label className="vgateway-form-field">
                  <span>Gateway name <b>*</b></span>
                  <input type="text" aria-invalid={Boolean(errors.name)} {...register("name")} />
                  {errors.name && <small role="alert">{errors.name.message}</small>}
                </label>
                <label className="vgateway-form-field">
                  <span>Gateway type <b>*</b></span>
                  <select disabled value="modbus_tcp" aria-label="Gateway type">
                    <option value="modbus_tcp">Modbus TCP</option>
                  </select>
                </label>
                <label className="vgateway-form-field">
                  <span>Description</span>
                  <textarea rows={3} {...register("description")} />
                  {errors.description && <small role="alert">{errors.description.message}</small>}
                </label>
                <label className="vgateway-switch-field">
                  <input type="checkbox" {...register("enabled")} />
                  <span className="vgateway-switch" aria-hidden="true"><span /></span>
                  <span><strong>Enabled</strong><small>Enable this gateway after saving.</small></span>
                </label>
              </div>
            </section>

            <section className="vgateway-form-section">
              <h2>Connection</h2>
              <div className="vgateway-form-grid is-two-column">
                <label className="vgateway-form-field">
                  <span>Host <b>*</b></span>
                  <input type="text" placeholder="192.168.1.100 or plc.local" aria-invalid={Boolean(errors.host)} {...register("host")} />
                  {errors.host && <small role="alert">{errors.host.message}</small>}
                </label>
                <label className="vgateway-form-field">
                  <span>Port <b>*</b></span>
                  <input type="number" inputMode="numeric" aria-invalid={Boolean(errors.port)} {...register("port", { valueAsNumber: true })} />
                  {errors.port && <small role="alert">{errors.port.message}</small>}
                </label>
              </div>
              <p className="vgateway-form-help">The host address and port used to connect to the Modbus TCP server.</p>
            </section>

            <section className="vgateway-form-section">
              <h2>Reliability &amp; timing</h2>
              <div className="vgateway-form-grid is-five-column">
                <label className="vgateway-form-field">
                  <span>Timeout (ms)</span>
                  <input type="number" inputMode="numeric" aria-invalid={Boolean(errors.timeout)} {...register("timeout", { valueAsNumber: true })} />
                  {errors.timeout && <small role="alert">{errors.timeout.message}</small>}
                </label>
                <label className="vgateway-form-field">
                  <span>Retry count</span>
                  <input type="number" inputMode="numeric" aria-invalid={Boolean(errors.retry_count)} {...register("retry_count", { valueAsNumber: true })} />
                  {errors.retry_count && <small role="alert">{errors.retry_count.message}</small>}
                </label>
                <label className="vgateway-form-field">
                  <span>Retry delay (ms)</span>
                  <input type="number" inputMode="numeric" aria-invalid={Boolean(errors.retry_delay)} {...register("retry_delay", { valueAsNumber: true })} />
                  {errors.retry_delay && <small role="alert">{errors.retry_delay.message}</small>}
                </label>
                <label className="vgateway-form-field">
                  <span>Reconnect interval (s)</span>
                  <input type="number" inputMode="numeric" aria-invalid={Boolean(errors.reconnect_interval)} {...register("reconnect_interval", { valueAsNumber: true })} />
                  {errors.reconnect_interval && <small role="alert">{errors.reconnect_interval.message}</small>}
                </label>
                <label className="vgateway-switch-field">
                  <input type="checkbox" {...register("keep_alive")} />
                  <span className="vgateway-switch" aria-hidden="true"><span /></span>
                  <span><strong>Keep alive</strong><small>Send periodic keep-alive messages.</small></span>
                </label>
              </div>
            </section>

            <section className="vgateway-form-section vgateway-test-section">
              <div>
                <h2>Connection test</h2>
                <p>{isEdit ? "Optionally probe one Modbus unit after opening a temporary connection." : "Save the gateway before testing the connection."}</p>
              </div>
              {isEdit && (
                <label className="vgateway-form-field vgateway-unit-field">
                  <span>Unit ID (optional)</span>
                  <input type="number" min="0" max="255" value={unitID} onChange={(event) => setUnitID(event.target.value)} />
                </label>
              )}
              <button className="vgateway-test-button" type="button" disabled={!isEdit || testing} onClick={() => void handleConnectionTest()}>
                {testing ? <LoaderCircle aria-hidden="true" className="is-spinning" size={18} /> : <Radio aria-hidden="true" size={18} />}
                {testing ? "Testing…" : "Test Connection"}
              </button>
              {testResult && (
                <div className={testResult.success ? "vgateway-test-result is-success" : "vgateway-test-result is-error"} role={testResult.success ? "status" : "alert"}>
                  {testResult.success ? <CheckCircle2 aria-hidden="true" size={19} /> : <CircleAlert aria-hidden="true" size={19} />}
                  <span>
                    <strong>{testResult.message}</strong>
                    {testResult.success && <small>{testResult.latency_ms.toFixed(2)} ms</small>}
                    {!testResult.success && <small>{testResult.error}</small>}
                  </span>
                </div>
              )}
            </section>

            <footer className="vgateway-form-actions">
              <button type="button" onClick={() => navigate("/vgateways")}>Cancel</button>
              <button type="submit" className="is-primary" disabled={isSubmitting}>
                {isSubmitting ? <LoaderCircle aria-hidden="true" className="is-spinning" size={18} /> : <Save aria-hidden="true" size={18} />}
                {isSubmitting ? "Saving…" : "Save Gateway"}
              </button>
            </footer>
          </form>
        )}
      </div>
    </VGatewayShell>
  );
}

export default VGatewayFormPage;
