import { zodResolver } from "@hookform/resolvers/zod";
import axios from "axios";
import {
  ArrowLeft,
  ArrowRight,
  CalendarClock,
  Check,
  CircleAlert,
  Clock3,
  DatabaseZap,
  HardDrive,
  LoaderCircle,
  Plus,
  Search,
  Tags,
  Trash2,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useFieldArray, useForm, useWatch } from "react-hook-form";
import { Link, useNavigate, useParams } from "react-router-dom";
import { z } from "zod";

import { createDataLogger, getDataLogger, updateDataLogger } from "../../../services/datalogger.service";
import { listTags } from "../../../services/tag.service";
import type { DataLogger, SaveDataLoggerRequest } from "../../../types/datalogger";
import type { Tag } from "../../../types/tag";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import { dateToLocalInput, formatInTimezone, isValidTimezone, nextDataLoggerRun, scheduleSummary, zonedDateTimeToDate } from "../utils/schedule";
import { bytesPerMiB, defaultDataLoggerLimitMiB, defaultEstimatedRowBytes, estimateDataLoggerStorage, formatEstimatedDuration, formatStorageBytes, maxDataLoggerLimitMiB } from "../utils/storage";
import "../../vgateway/pages/VGatewayFormPage.css";
import "./DataLoggerListPage.css";
import "./DataLoggerWizardPage.css";

const maxIntervalSeconds = 9_223_372_036;
const maxScheduleEvery = 2_562_047;
const maxRetentionAgeSeconds = 10 * 365 * 24 * 60 * 60;
const timePattern = /^([01]\d|2[0-3]):[0-5]\d$/;
const steps = ["Basics", "Tags", "Schedule", "Review"];
const weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];
const retentionAgeUnits = {
  second: 1,
  minute: 60,
  hour: 60 * 60,
  day: 24 * 60 * 60,
  week: 7 * 24 * 60 * 60,
} as const;

type RetentionAgeUnit = keyof typeof retentionAgeUnits;

const formSchema = z.object({
  name: z.string().trim().min(1, "Name is required").max(100, "Name must be 100 characters or fewer"),
  description: z.string(),
  enabled: z.boolean(),
  timezone: z.string().refine(isValidTimezone, "Enter a valid IANA timezone"),
  mode: z.enum(["interval", "schedule"]),
  start_local: z.string().min(1, "Start date and time are required"),
  end_enabled: z.boolean(),
  end_local: z.string(),
  storage_limit_enabled: z.boolean(),
  max_size_mib: z.number().int().min(1).max(maxDataLoggerLimitMiB),
  age_limit_enabled: z.boolean(),
  max_age_amount: z.number().int().min(1),
  max_age_unit: z.enum(["second", "minute", "hour", "day", "week"]),
  interval_seconds: z.number().int().min(1).max(maxIntervalSeconds),
  schedule_unit: z.enum(["minute", "hour", "day", "week"]),
  schedule_every: z.number().int().min(1).max(maxScheduleEvery),
  times: z.array(z.object({ value: z.string() })),
  weekdays: z.array(z.number().int().min(1).max(7)),
}).superRefine((values, context) => {
  const start = zonedDateTimeToDate(values.start_local, values.timezone);
  if (!start) context.addIssue({ code: "custom", path: ["start_local"], message: "This local time does not exist in the selected timezone" });
  if (values.end_enabled) {
    const end = zonedDateTimeToDate(values.end_local, values.timezone);
    if (!end) context.addIssue({ code: "custom", path: ["end_local"], message: "Enter a valid end date and time" });
    else if (start && end <= start) context.addIssue({ code: "custom", path: ["end_local"], message: "End must be after start" });
  }
  if (values.mode === "schedule" && (values.schedule_unit === "day" || values.schedule_unit === "week")) {
    if (values.times.length === 0) context.addIssue({ code: "custom", path: ["times"], message: "Add at least one schedule time" });
    values.times.forEach((time, index) => {
      if (!timePattern.test(time.value)) context.addIssue({ code: "custom", path: ["times", index, "value"], message: "Use HH:MM" });
    });
    if (new Set(values.times.map((time) => time.value)).size !== values.times.length) context.addIssue({ code: "custom", path: ["times"], message: "Schedule times must be unique" });
  }
  if (values.mode === "schedule" && values.schedule_unit === "week" && values.weekdays.length === 0) context.addIssue({ code: "custom", path: ["weekdays"], message: "Select at least one weekday" });
  if (values.age_limit_enabled && values.max_age_amount * retentionAgeUnits[values.max_age_unit] > maxRetentionAgeSeconds) context.addIssue({ code: "custom", path: ["max_age_amount"], message: "Age retention cannot exceed 10 years" });
});

type FormValues = z.infer<typeof formSchema>;

function timezoneValues(): string[] {
  const extended = Intl as typeof Intl & { supportedValuesOf?: (key: "timeZone") => string[] };
  return extended.supportedValuesOf?.("timeZone") ?? ["UTC", "Asia/Bangkok", "Asia/Singapore", "Europe/London", "America/New_York"];
}

function localTimezone(): string {
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return timezone && isValidTimezone(timezone) ? timezone : "UTC";
}

function initialValues(): FormValues {
  const timezone = localTimezone();
  const start = new Date(Date.now() + 60 * 60 * 1000);
  start.setUTCSeconds(0, 0);
  return { name: "", description: "", enabled: true, timezone, mode: "interval", start_local: dateToLocalInput(start, timezone), end_enabled: false, end_local: "", storage_limit_enabled: true, max_size_mib: defaultDataLoggerLimitMiB, age_limit_enabled: false, max_age_amount: 30, max_age_unit: "day", interval_seconds: 60, schedule_unit: "day", schedule_every: 1, times: [{ value: "08:00" }], weekdays: [1, 2, 3, 4, 5] };
}

function retentionAgeValue(seconds: number | null | undefined): { amount: number; unit: RetentionAgeUnit } {
  if (!seconds) return { amount: 30, unit: "day" };
  const units = Object.entries(retentionAgeUnits).reverse() as [RetentionAgeUnit, number][];
  const exact = units.find(([, multiplier]) => seconds % multiplier === 0);
  return exact ? { amount: seconds / exact[1], unit: exact[0] } : { amount: seconds, unit: "second" };
}

function valuesFromLogger(logger: DataLogger): FormValues {
  const schedule = "unit" in logger.config ? logger.config : null;
  const maxAge = retentionAgeValue(logger.max_age_seconds);
  return {
    name: logger.name,
    description: logger.description ?? "",
    enabled: logger.enabled,
    timezone: logger.timezone,
    mode: logger.mode,
    start_local: dateToLocalInput(logger.start_at, logger.timezone),
    end_enabled: Boolean(logger.end_at),
    end_local: logger.end_at ? dateToLocalInput(logger.end_at, logger.timezone) : "",
    storage_limit_enabled: logger.max_size_bytes !== null,
    max_size_mib: logger.max_size_bytes === null ? defaultDataLoggerLimitMiB : Math.max(1, Math.round(logger.max_size_bytes / bytesPerMiB)),
    age_limit_enabled: logger.max_age_seconds != null,
    max_age_amount: maxAge.amount,
    max_age_unit: maxAge.unit,
    interval_seconds: "interval_seconds" in logger.config ? logger.config.interval_seconds : 60,
    schedule_unit: schedule?.unit ?? "day",
    schedule_every: schedule?.every ?? 1,
    times: (schedule?.times?.length ? schedule.times : ["08:00"]).map((value) => ({ value })),
    weekdays: schedule?.weekdays ?? [1, 2, 3, 4, 5],
  };
}

function requestFromValues(values: FormValues, tagIDs: string[]): SaveDataLoggerRequest {
  const start = zonedDateTimeToDate(values.start_local, values.timezone);
  const end = values.end_enabled ? zonedDateTimeToDate(values.end_local, values.timezone) : null;
  if (!start || values.end_enabled && !end) throw new Error("Schedule boundary is invalid");
  const config = values.mode === "interval"
    ? { interval_seconds: values.interval_seconds }
    : {
        unit: values.schedule_unit,
        every: values.schedule_every,
        ...((values.schedule_unit === "day" || values.schedule_unit === "week") ? { times: values.times.map((time) => time.value).sort() } : {}),
        ...(values.schedule_unit === "week" ? { weekdays: [...values.weekdays].sort((first, second) => first - second) } : {}),
      };
  return { name: values.name.trim(), description: values.description.trim() || null, enabled: values.enabled, timezone: values.timezone, mode: values.mode, start_at: start.toISOString(), end_at: end?.toISOString() ?? null, max_size_bytes: values.storage_limit_enabled ? values.max_size_mib * bytesPerMiB : null, max_age_seconds: values.age_limit_enabled ? values.max_age_amount * retentionAgeUnits[values.max_age_unit] : null, config, tag_ids: tagIDs };
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return error instanceof Error ? error.message : "Unable to save Data Logger";
}

async function loadAllTags(signal: AbortSignal): Promise<Tag[]> {
  const tags: Tag[] = [];
  let page = 1;
  while (true) {
    const response = await listTags({ page, per_page: 100 }, signal);
    tags.push(...response.data);
    if (page >= response.pagination.total_pages) return tags;
    page += 1;
  }
}

function DataLoggerWizardPage() {
  const { id } = useParams();
  const editing = Boolean(id);
  const navigate = useNavigate();
  const [step, setStep] = useState(0);
  const [availableTags, setAvailableTags] = useState<Tag[]>([]);
  const [selectedTagIDs, setSelectedTagIDs] = useState<string[]>([]);
  const [tagSearch, setTagSearch] = useState("");
  const [tagError, setTagError] = useState<string | null>(null);
  const [loadState, setLoadState] = useState<"loading" | "ready" | "error">(editing ? "loading" : "ready");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [averageRowBytes, setAverageRowBytes] = useState(defaultEstimatedRowBytes);
  const { register, control, reset, setValue, trigger, handleSubmit, formState: { errors } } = useForm<FormValues>({ resolver: zodResolver(formSchema), defaultValues: initialValues() });
  const times = useFieldArray({ control, name: "times" });
  const values = useWatch({ control }) as FormValues;
  const mode = useWatch({ control, name: "mode" });
  const scheduleUnit = useWatch({ control, name: "schedule_unit" });
  const selectedWeekdays = useWatch({ control, name: "weekdays" });

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([loadAllTags(controller.signal), id ? getDataLogger(id, controller.signal) : Promise.resolve(null)]).then(([tags, logger]) => {
      if (controller.signal.aborted) return;
      setAvailableTags(tags);
      if (logger) {
        reset(valuesFromLogger(logger));
        setSelectedTagIDs((logger.tags ?? []).map((tag) => tag.id));
        setAverageRowBytes(logger.storage?.average_row_bytes ?? defaultEstimatedRowBytes);
      }
      setLoadState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setLoadError(errorMessage(error));
      setLoadState("error");
    });
    return () => controller.abort();
  }, [id, reset]);

  const filteredTags = useMemo(() => {
    const query = tagSearch.trim().toLowerCase();
    return query ? availableTags.filter((tag) => tag.name.toLowerCase().includes(query) || tag.data_type.includes(query) || tag.type.includes(query)) : availableTags;
  }, [availableTags, tagSearch]);

  const preview = useMemo(() => {
    try {
      const request = requestFromValues(values, selectedTagIDs);
      const next = nextDataLoggerRun({
        mode: request.mode,
        config: request.config,
        start_at: request.start_at,
        end_at: request.end_at,
        timezone: request.timezone,
      });
      return { request, next };
    } catch {
      return null;
    }
  }, [selectedTagIDs, values]);
  const storageEstimate = useMemo(() => preview ? estimateDataLoggerStorage({ maxSizeBytes: preview.request.max_size_bytes, tagCount: selectedTagIDs.length, averageRowBytes, batchCount: 0, mode: preview.request.mode, config: preview.request.config }) : null, [averageRowBytes, preview, selectedTagIDs.length]);

  function toggleTag(tagID: string) {
    setTagError(null);
    setSelectedTagIDs((current) => current.includes(tagID) ? current.filter((id) => id !== tagID) : [...current, tagID]);
  }

  async function nextStep() {
    if (step === 0 && !await trigger(["name", "description", "enabled"])) return;
    if (step === 1 && selectedTagIDs.length === 0) { setTagError("Select at least one Tag"); return; }
    if (step === 2 && !await trigger()) return;
    setStep((current) => Math.min(3, current + 1));
  }

  async function submit(valuesToSave: FormValues) {
    if (step !== 3 || selectedTagIDs.length === 0) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const request = requestFromValues(valuesToSave, selectedTagIDs);
      if (id) await updateDataLogger(id, request); else await createDataLogger(request);
      navigate("/data-loggers");
    } catch (error) {
      setSubmitError(errorMessage(error));
    } finally {
      setSubmitting(false);
    }
  }

  if (loadState !== "ready") return <VGatewayShell breadcrumb={<>Data Loggers <span>/</span> <strong>{editing ? "Edit" : "New"}</strong></>}><div className="datalogger-wizard-content"><div className={`datalogger-wizard-state ${loadState === "error" ? "is-error" : ""}`} role={loadState === "error" ? "alert" : "status"}>{loadState === "loading" ? <LoaderCircle className="is-spinning" /> : <CircleAlert />}<strong>{loadState === "loading" ? "Loading Data Logger…" : "Couldn’t load Data Logger"}</strong>{loadError && <span>{loadError}</span>}</div></div></VGatewayShell>;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Loggers <span>/</span> <strong>{editing ? "Edit" : "New"}</strong></>}>
    <div className="datalogger-wizard-content">
      <header className="datalogger-wizard-heading"><Link to="/data-loggers"><ArrowLeft size={17} /> Data Loggers</Link><h1>{editing ? "Edit Data Logger" : "New Data Logger"}</h1><p>Build a synchronized Tag snapshot schedule without changing datasource polling.</p></header>
      <ol className="datalogger-steps">{steps.map((label, index) => <li key={label} className={index === step ? "is-current" : index < step ? "is-complete" : ""}><span>{index < step ? <Check size={15} /> : index + 1}</span><strong>{label}</strong></li>)}</ol>

      <form className="datalogger-wizard-form" onSubmit={handleSubmit(submit)}>
        {step === 0 && <section className="datalogger-wizard-panel"><div className="datalogger-panel-heading"><DatabaseZap /><div><h2>Logger identity</h2><p>Name the acquisition engine and choose whether it starts automatically.</p></div></div><div className="datalogger-form-grid">
          <label className="is-wide"><span>Name</span><input autoFocus aria-invalid={Boolean(errors.name)} {...register("name")} />{errors.name && <small className="is-error">{errors.name.message}</small>}</label>
          <label className="is-wide"><span>Description <small>Optional</small></span><textarea rows={3} {...register("description")} /></label>
          <label className="datalogger-switch-field"><input type="checkbox" {...register("enabled")} /><span className="datalogger-switch"><span /></span><span><strong>Enable Data Logger</strong><small>Runtime starts this definition automatically.</small></span></label>
        </div></section>}

        {step === 1 && <section className="datalogger-wizard-panel"><div className="datalogger-panel-heading"><Tags /><div><h2>Select Tags</h2><p>Every run snapshots these latest normalized values with one shared timestamp.</p></div><strong className="datalogger-selection-count">{selectedTagIDs.length} selected</strong></div>
          <div className="datalogger-tag-search"><Search size={17} /><label className="sr-only" htmlFor="logger-tag-search">Search Tags</label><input id="logger-tag-search" value={tagSearch} onChange={(event) => setTagSearch(event.target.value)} placeholder="Search name, type, or data type…" /></div>
          {tagError && <p className="datalogger-field-error" role="alert">{tagError}</p>}
          <div className="datalogger-tag-selector">{filteredTags.map((tag) => <label key={tag.id} className={selectedTagIDs.includes(tag.id) ? "is-selected" : ""}><input type="checkbox" checked={selectedTagIDs.includes(tag.id)} onChange={() => toggleTag(tag.id)} /><span><strong>{tag.name}</strong><small>{tag.type} · {tag.data_type}{tag.enabled ? "" : " · disabled"}</small></span><Check size={16} /></label>)}{filteredTags.length === 0 && <div className="datalogger-tag-empty">No matching Tags</div>}</div>
        </section>}

        {step === 2 && <section className="datalogger-wizard-panel"><div className="datalogger-panel-heading"><CalendarClock /><div><h2>Capture schedule</h2><p>Logger timing is independent from datasource polling and never sends device requests.</p></div></div>
          <div className="datalogger-mode-picker"><label className={mode === "interval" ? "is-selected" : ""}><input type="radio" value="interval" {...register("mode")} /><Clock3 /><span><strong>Fixed interval</strong><small>Anchored cadence in seconds</small></span></label><label className={mode === "schedule" ? "is-selected" : ""}><input type="radio" value="schedule" {...register("mode")} /><CalendarClock /><span><strong>Calendar schedule</strong><small>Timezone-aware wall clock</small></span></label></div>
          <div className="datalogger-form-grid schedule-grid">
            <label><span>Timezone</span><input list="datalogger-timezones" aria-invalid={Boolean(errors.timezone)} {...register("timezone")} /><datalist id="datalogger-timezones">{timezoneValues().map((timezone) => <option key={timezone} value={timezone} />)}</datalist>{errors.timezone && <small className="is-error">{errors.timezone.message}</small>}</label>
            <label><span>Start date & time</span><input type="datetime-local" aria-invalid={Boolean(errors.start_local)} {...register("start_local")} />{errors.start_local && <small className="is-error">{errors.start_local.message}</small>}</label>
            <label className="datalogger-end-toggle"><span>End boundary</span><span><input type="checkbox" {...register("end_enabled")} /> Enable end date and time</span></label>
            {values.end_enabled && <label><span>End date & time</span><input type="datetime-local" aria-invalid={Boolean(errors.end_local)} {...register("end_local")} />{errors.end_local && <small className="is-error">{errors.end_local.message}</small>}</label>}
            {mode === "interval" && <label><span>Capture every (seconds)</span><input type="number" min="1" max={maxIntervalSeconds} aria-invalid={Boolean(errors.interval_seconds)} {...register("interval_seconds", { valueAsNumber: true })} />{errors.interval_seconds && <small className="is-error">Enter a positive whole number</small>}</label>}
            {mode === "schedule" && <><label><span>Calendar unit</span><select {...register("schedule_unit")}><option value="minute">Minute</option><option value="hour">Hour</option><option value="day">Day</option><option value="week">Week</option></select></label><label><span>Repeat every</span><input type="number" min="1" max={maxScheduleEvery} {...register("schedule_every", { valueAsNumber: true })} />{errors.schedule_every && <small className="is-error">Enter a positive whole number</small>}</label></>}
          </div>
          {mode === "schedule" && (scheduleUnit === "day" || scheduleUnit === "week") && <div className="datalogger-calendar-options">
            {scheduleUnit === "week" && <fieldset><legend>Weekdays</legend><div className="datalogger-weekdays">{weekdays.map((day, index) => { const value = index + 1; return <label key={day} className={selectedWeekdays.includes(value) ? "is-selected" : ""}><input type="checkbox" checked={selectedWeekdays.includes(value)} onChange={() => setValue("weekdays", selectedWeekdays.includes(value) ? selectedWeekdays.filter((item) => item !== value) : [...selectedWeekdays, value], { shouldValidate: true })} />{day.slice(0, 3)}</label>; })}</div>{errors.weekdays && <small className="is-error">{errors.weekdays.message}</small>}</fieldset>}
            <fieldset><legend>Wall-clock times</legend><div className="datalogger-times">{times.fields.map((field, index) => <label key={field.id}><span>Run {index + 1}</span><input type="time" aria-label={`Schedule time ${index + 1}`} {...register(`times.${index}.value`)} /><button type="button" aria-label={`Remove schedule time ${index + 1}`} disabled={times.fields.length === 1} onClick={() => times.remove(index)}><Trash2 size={15} /></button>{errors.times?.[index]?.value && <small className="is-error">{errors.times[index]?.value?.message}</small>}</label>)}<button type="button" onClick={() => times.append({ value: "12:00" })}><Plus size={16} /> Add time</button></div>{errors.times?.root && <small className="is-error">{errors.times.root.message}</small>}</fieldset>
          </div>}
          <section className="datalogger-storage-limit" aria-labelledby="datalogger-storage-heading">
            <div className="datalogger-panel-heading"><HardDrive /><div><h3 id="datalogger-storage-heading">Rolling retention policy</h3><p>Keep complete synchronized batches and remove the oldest batch when either configured limit is exceeded.</p></div></div>
            <div className="datalogger-storage-controls">
              <label className="datalogger-switch-field"><input type="checkbox" {...register("storage_limit_enabled")} /><span className="datalogger-switch"><span /></span><span><strong>Limit history by size</strong><small>Disable to keep history without a size quota.</small></span></label>
              <label><span>Maximum storage (MiB)</span><input type="number" min="1" max={maxDataLoggerLimitMiB} disabled={!values.storage_limit_enabled} aria-invalid={Boolean(errors.max_size_mib)} {...register("max_size_mib", { valueAsNumber: true })} />{errors.max_size_mib && <small className="is-error">Enter 1 MiB to 1 TiB</small>}</label>
            </div>
            <div className="datalogger-storage-controls datalogger-age-controls">
              <label className="datalogger-switch-field"><input type="checkbox" {...register("age_limit_enabled")} /><span className="datalogger-switch"><span /></span><span><strong>Limit history by age</strong><small>Remove complete batches older than this rolling window.</small></span></label>
              <div className="datalogger-duration-field"><label><span>Maximum age</span><input type="number" min="1" disabled={!values.age_limit_enabled} aria-invalid={Boolean(errors.max_age_amount)} {...register("max_age_amount", { valueAsNumber: true })} /></label><label><span>Unit</span><select disabled={!values.age_limit_enabled} {...register("max_age_unit")}><option value="second">Seconds</option><option value="minute">Minutes</option><option value="hour">Hours</option><option value="day">Days</option><option value="week">Weeks</option></select></label>{errors.max_age_amount && <small className="is-error">{errors.max_age_amount.message}</small>}</div>
            </div>
            {storageEstimate ? <div className="datalogger-storage-estimate" aria-label="Estimated storage retention"><span><strong>{storageEstimate.capacityRows.toLocaleString()}</strong> rows</span><span><strong>{storageEstimate.capacityBatches.toLocaleString()}</strong> complete batches</span><span><strong>{formatEstimatedDuration(Math.min(storageEstimate.estimatedRetentionSeconds, preview?.request.max_age_seconds ?? Number.POSITIVE_INFINITY))}</strong> retained history</span></div> : values.age_limit_enabled ? <div className="datalogger-storage-unlimited">Age retention · keep approximately {formatEstimatedDuration(values.max_age_amount * retentionAgeUnits[values.max_age_unit])}</div> : <div className="datalogger-storage-unlimited">Unlimited retention · no automatic pruning</div>}
            <small className="datalogger-storage-note">Estimate uses {formatStorageBytes(averageRowBytes)} per row and updates from actual Logger history after capture. PostgreSQL disk allocation may be higher.</small>
          </section>
        </section>}

        {step === 3 && <section className="datalogger-wizard-panel"><div className="datalogger-panel-heading"><Check /><div><h2>Review and save</h2><p>Confirm the definition the runtime will reconcile automatically.</p></div></div>{preview ? <div className="datalogger-review-grid">
          <article><span>Name</span><strong>{preview.request.name}</strong><small>{preview.request.enabled ? "Enabled" : "Paused"}</small></article>
          <article><span>Tags</span><strong>{selectedTagIDs.length}</strong><small>{availableTags.filter((tag) => selectedTagIDs.includes(tag.id)).map((tag) => tag.name).join(", ")}</small></article>
          <article><span>Cadence</span><strong>{preview.request.mode === "interval" ? "Fixed interval" : "Calendar"}</strong><small>{scheduleSummary(preview.request.mode, preview.request.config)}</small></article>
          <article><span>Next run</span><strong>{preview.next ? formatInTimezone(preview.next, preview.request.timezone) : "No future run"}</strong><small>{preview.request.timezone}</small></article>
          <article><span>Retention</span><strong>{preview.request.max_size_bytes === null ? "No size limit" : formatStorageBytes(preview.request.max_size_bytes)}</strong><small>{preview.request.max_age_seconds == null ? "No age limit" : `Maximum age ${formatEstimatedDuration(preview.request.max_age_seconds)}`}</small></article>
          <article className="is-wide"><span>Data path</span><strong>Latest Tag snapshot → raw history</strong><small>No datasource read is triggered by this engine.</small></article>
        </div> : <div className="datalogger-alert" role="alert"><CircleAlert size={18} />Schedule preview is unavailable. Go back and correct the schedule.</div>}{submitError && <div className="datalogger-alert" role="alert"><CircleAlert size={18} />{submitError}</div>}</section>}

        <footer className="datalogger-wizard-actions"><button type="button" onClick={() => step === 0 ? navigate("/data-loggers") : setStep((current) => current - 1)}><ArrowLeft size={17} />{step === 0 ? "Cancel" : "Back"}</button>{step < 3 ? <button className="is-primary" type="button" onClick={(event) => { event.preventDefault(); void nextStep(); }}>Continue <ArrowRight size={17} /></button> : <button className="is-primary" type="submit" disabled={submitting || !preview}>{submitting ? <LoaderCircle className="is-spinning" size={17} /> : <DatabaseZap size={17} />}{editing ? "Save changes" : "Create Data Logger"}</button>}</footer>
      </form>
    </div>
  </VGatewayShell>;
}

export default DataLoggerWizardPage;
