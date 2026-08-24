import { zodResolver } from "@hookform/resolvers/zod";
import axios from "axios";
import { Circle, CircleAlert, CircleCheck, Clock3, Eye, EyeOff, KeyRound, LoaderCircle, ShieldCheck, UserRound } from "lucide-react";
import { useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { useNavigate } from "react-router-dom";
import { z } from "zod";

import { changePassword } from "../../../services/auth.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { ApiErrorResponse } from "../../../types/auth";
import { passwordLength, passwordRequirementStates } from "../../auth/utils/password";
import SettingsWorkspace from "../components/SettingsWorkspace";

const accountDateFormatter = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

const changePasswordSchema = z.object({
  current_password: z.string().min(1, "Current password is required"),
  new_password: z.string().min(1, "New password is required"),
  confirm_password: z.string().min(1, "Confirm password is required"),
}).superRefine((values, context) => {
  const length = passwordLength(values.new_password);
  const requirements = passwordRequirementStates(values.new_password, "");
  if (values.new_password.length > 0 && length < 8) context.addIssue({ code: "custom", message: "New password must be at least 8 characters", path: ["new_password"] });
  if (length > 128) context.addIssue({ code: "custom", message: "New password must be at most 128 characters", path: ["new_password"] });
  if (!requirements[1].valid) context.addIssue({ code: "custom", message: "New password must contain at least 3 character types", path: ["new_password"] });
  if (values.current_password && values.current_password === values.new_password) context.addIssue({ code: "custom", message: "New password must be different from current password", path: ["new_password"] });
  if (values.confirm_password !== values.new_password) context.addIssue({ code: "custom", message: "Passwords do not match", path: ["confirm_password"] });
});

type ChangePasswordFormValues = z.infer<typeof changePasswordSchema>;

function formatAccountDate(value: string | null): string {
  if (!value) return "Not recorded";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Not recorded" : accountDateFormatter.format(date);
}

function passwordError(error: unknown): { code: string | null; message: string } {
  if (axios.isAxiosError<ApiErrorResponse>(error)) {
    const code = error.response?.data?.error?.code ?? null;
    if (code === "AUTH001") return { code, message: "Current password is incorrect." };
    if (code === "AUTH002") return { code, message: "Account is locked. Sign in again after the lock expires." };
    if (code === "AUTH006") return { code, message: "The new password does not meet the password requirements." };
    if (code === "AUTH007") return { code, message: "This password was used recently. Choose a different password." };
    if (code === "AUTH004" || code === "AUTH005") return { code, message: "Your session expired. Sign in again." };
  }
  return { code: null, message: "Unable to change password. Check the connection and try again." };
}

interface PasswordFieldProps {
  autoComplete: "current-password" | "new-password";
  describedBy?: string;
  error?: string;
  id: string;
  label: string;
  name: keyof ChangePasswordFormValues;
  register: ReturnType<typeof useForm<ChangePasswordFormValues>>["register"];
}

function PasswordField({ autoComplete, describedBy, error, id, label, name, register }: PasswordFieldProps) {
  const [visible, setVisible] = useState(false);
  return (
    <div className="settings-password-field">
      <label htmlFor={id}>{label}</label>
      <div>
        <input id={id} type={visible ? "text" : "password"} autoComplete={autoComplete} aria-invalid={Boolean(error)} aria-describedby={[error ? `${id}-error` : "", describedBy ?? ""].filter(Boolean).join(" ") || undefined} {...register(name)} />
        <button type="button" aria-label={visible ? `Hide ${label.toLocaleLowerCase()}` : `Show ${label.toLocaleLowerCase()}`} aria-pressed={visible} onClick={() => setVisible((current) => !current)}>{visible ? <EyeOff aria-hidden="true" /> : <Eye aria-hidden="true" />}</button>
      </div>
      {error && <p id={`${id}-error`}>{error}</p>}
    </div>
  );
}

function AccountSettingsPage() {
  const navigate = useNavigate();
  const user = useAuthStore((state) => state.user);
  const clearSession = useAuthStore((state) => state.clearSession);
  const [requestError, setRequestError] = useState<string | null>(null);
  const { control, formState: { errors, isSubmitting }, handleSubmit, register, reset, setError } = useForm<ChangePasswordFormValues>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { current_password: "", new_password: "", confirm_password: "" },
  });
  const newPassword = useWatch({ control, name: "new_password" });
  const requirements = passwordRequirementStates(newPassword, user?.username ?? "");

  const submit = handleSubmit(async (values) => {
    if (!passwordRequirementStates(values.new_password, user?.username ?? "")[2].valid) {
      setError("new_password", { message: "New password must not contain your username" });
      return;
    }
    setRequestError(null);
    try {
      await changePassword(values);
      reset();
      clearSession();
      navigate("/login", { replace: true, state: { passwordChanged: true } });
    } catch (error) {
      reset();
      const failure = passwordError(error);
      if (failure.code === "AUTH004" || failure.code === "AUTH005") {
        clearSession();
        navigate("/login", { replace: true, state: { sessionExpired: true } });
        return;
      }
      setRequestError(failure.message);
    }
  });

  return (
    <SettingsWorkspace section="Account">
      <section className="settings-section-heading">
        <div><p>Local administrator</p><h2>Account</h2><span>Review the authenticated account and rotate its password without exposing credential or session material.</span></div>
      </section>

      {!user ? (
        <section className="settings-state-panel"><div className="settings-state"><UserRound /><strong>Account details unavailable</strong><span>Restore the authenticated session to load this protected account workspace.</span></div></section>
      ) : (
        <div className="settings-account-grid">
          <section className="settings-profile-card" aria-labelledby="settings-profile-heading">
            <header><UserRound aria-hidden="true" /><div><p>Signed in as</p><h3 id="settings-profile-heading">{user.username}</h3></div></header>
            <dl><div><dt>Account created</dt><dd>{formatAccountDate(user.created_at)}</dd></div><div><dt>Last login</dt><dd>{formatAccountDate(user.last_login)}</dd></div></dl>
            <div className="settings-session-warning"><Clock3 aria-hidden="true" /><span><strong>Fresh login required</strong><small>A successful change invalidates every existing refresh session and returns this browser to Login.</small></span></div>
          </section>

          <section className="settings-password-card" aria-labelledby="settings-password-heading">
            <header><ShieldCheck aria-hidden="true" /><div><p>Credential security</p><h3 id="settings-password-heading">Change password</h3></div></header>
            <form onSubmit={submit} noValidate>
              {requestError && <div className="settings-password-error" role="alert"><CircleAlert aria-hidden="true" /><span>{requestError}</span></div>}
              <PasswordField id="current-password" name="current_password" label="Current password" autoComplete="current-password" error={errors.current_password?.message} register={register} />
              <PasswordField id="new-password" name="new_password" label="New password" autoComplete="new-password" describedBy="settings-password-requirements" error={errors.new_password?.message} register={register} />
              <PasswordField id="confirm-password" name="confirm_password" label="Confirm new password" autoComplete="new-password" error={errors.confirm_password?.message} register={register} />

              <div className="settings-password-requirements" id="settings-password-requirements">
                <h4>Password requirements</h4>
                <ul>{requirements.map((requirement) => <li className={requirement.valid ? "is-valid" : ""} key={requirement.label}>{requirement.valid ? <CircleCheck aria-hidden="true" /> : <Circle aria-hidden="true" />}<span>{requirement.label}</span></li>)}</ul>
                <p><KeyRound aria-hidden="true" />The new password also cannot match your current or two previous passwords.</p>
              </div>

              <button className="settings-password-submit" type="submit" disabled={isSubmitting}>{isSubmitting ? <><LoaderCircle className="is-spinning" aria-hidden="true" />Changing password…</> : <><KeyRound aria-hidden="true" />Change password</>}</button>
            </form>
          </section>
        </div>
      )}
    </SettingsWorkspace>
  );
}

export default AccountSettingsPage;
