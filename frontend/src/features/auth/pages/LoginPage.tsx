import { useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import axios from "axios";
import {
  CircleAlert,
  Eye,
  EyeOff,
  LoaderCircle,
} from "lucide-react";
import { useForm } from "react-hook-form";
import { useLocation, useNavigate } from "react-router-dom";
import { z } from "zod";

import desktopArtwork from "../../../assets/login-industrial-network.jpg";
import mobileArtwork from "../../../assets/login-industrial-network-mobile.jpg";
import { getMe, login } from "../../../services/auth.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { ApiErrorResponse } from "../../../types/auth";
import AuthBrand from "../components/AuthBrand";
import "./LoginPage.css";

const loginSchema = z.object({
  username: z
    .string()
    .trim()
    .min(1, "Username is required")
    .min(3, "Username must be at least 3 characters"),
  password: z.string().min(1, "Password is required"),
});

type LoginFormValues = z.infer<typeof loginSchema>;

interface LoginLocationState {
  from?: {
    hash?: string;
    pathname?: string;
    search?: string;
  };
}

function getLoginDestination(state: unknown): string {
  const from = (state as LoginLocationState | null)?.from;

  if (!from?.pathname || from.pathname === "/login" || from.pathname === "/setup") {
    return "/dashboard";
  }

  return `${from.pathname}${from.search ?? ""}${from.hash ?? ""}`;
}

function getLoginErrorMessage(error: unknown): string {
  if (axios.isAxiosError<ApiErrorResponse>(error)) {
    const message = error.response?.data?.error?.message;

    if (message) {
      return message;
    }
  }

  return "Unable to sign in. Check your connection and try again.";
}

function LoginPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const [showPassword, setShowPassword] = useState(false);
  const [requestError, setRequestError] = useState<string | null>(null);
  const setAccessToken = useAuthStore((state) => state.setAccessToken);
  const setSession = useAuthStore((state) => state.setSession);
  const setLoading = useAuthStore((state) => state.setLoading);
  const clearSession = useAuthStore((state) => state.clearSession);
  const {
    formState: { errors, isSubmitting },
    handleSubmit,
    register,
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: {
      username: "",
      password: "",
    },
  });

  const onSubmit = handleSubmit(async (values) => {
    setRequestError(null);
    setLoading(true);

    try {
      const tokenResponse = await login(values);
      setAccessToken(tokenResponse.access_token);

      const user = await getMe();
      setSession(
        user,
        tokenResponse.access_token,
        tokenResponse.expires_in,
      );
      navigate(getLoginDestination(location.state), { replace: true });
    } catch (error) {
      clearSession();
      setRequestError(getLoginErrorMessage(error));
    } finally {
      setLoading(false);
    }
  });

  return (
    <main className="login-page">
      <section className="login-visual" aria-label="IoT Edge">
        <picture>
          <source media="(max-width: 767px)" srcSet={mobileArtwork} />
          <img
            className="login-artwork"
            src={desktopArtwork}
            alt=""
            aria-hidden="true"
          />
        </picture>

        <AuthBrand />
      </section>

      <section className="login-workspace">
        <div className="login-form-shell">
          <header className="login-heading">
            <h1>Welcome back</h1>
            <p>Sign in to manage your edge network.</p>
          </header>

          <form className="login-form" onSubmit={onSubmit} noValidate>
            {requestError && (
              <div className="login-request-error" role="alert">
                <CircleAlert aria-hidden="true" size={18} />
                <span>{requestError}</span>
              </div>
            )}

            <div className="login-field">
              <label htmlFor="username">Username</label>
              <input
                id="username"
                type="text"
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                aria-invalid={Boolean(errors.username)}
                aria-describedby={errors.username ? "username-error" : undefined}
                autoFocus
                {...register("username")}
              />
              {errors.username && (
                <p id="username-error" className="login-field-error">
                  {errors.username.message}
                </p>
              )}
            </div>

            <div className="login-field">
              <label htmlFor="password">Password</label>
              <div className="login-password-control">
                <input
                  id="password"
                  type={showPassword ? "text" : "password"}
                  autoComplete="current-password"
                  aria-invalid={Boolean(errors.password)}
                  aria-describedby={errors.password ? "password-error" : undefined}
                  {...register("password")}
                />
                <button
                  type="button"
                  className="login-password-toggle"
                  onClick={() => setShowPassword((current) => !current)}
                  aria-label={showPassword ? "Hide password" : "Show password"}
                  aria-pressed={showPassword}
                >
                  {showPassword ? (
                    <EyeOff aria-hidden="true" size={22} />
                  ) : (
                    <Eye aria-hidden="true" size={22} />
                  )}
                </button>
              </div>
              {errors.password && (
                <p id="password-error" className="login-field-error">
                  {errors.password.message}
                </p>
              )}
            </div>

            <button
              type="submit"
              className="login-submit"
              disabled={isSubmitting}
            >
              {isSubmitting ? (
                <>
                  <LoaderCircle
                    aria-hidden="true"
                    className="login-spinner"
                    size={20}
                  />
                  Signing in…
                </>
              ) : (
                "Sign in"
              )}
            </button>
          </form>
        </div>
      </section>
    </main>
  );
}

export default LoginPage;
