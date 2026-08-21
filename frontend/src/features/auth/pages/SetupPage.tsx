import { useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import axios from "axios";
import {
    CircleAlert,
    Circle,
    CircleCheck,
    Eye,
    EyeOff,
    LoaderCircle,
} from "lucide-react";
import { useForm, useWatch } from "react-hook-form";
import { useNavigate } from "react-router-dom";
import { z } from "zod";

import desktopArtwork from "../../../assets/login-industrial-network.jpg";
import mobileArtwork from "../../../assets/login-industrial-network-mobile.jpg";
import { setup } from "../../../services/auth.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { ApiErrorResponse } from "../../../types/auth";
import AuthBrand from "../components/AuthBrand";
import "./LoginPage.css";

function countPasswordClasses(password: string): number {
    return [
        /[A-Z]/.test(password),
        /[a-z]/.test(password),
        /\d/.test(password),
        /[^A-Za-z0-9]/.test(password),
    ].filter(Boolean).length;
}

const setupSchema = z
    .object({
        username: z
            .string()
            .trim()
            .min(1, "Username is required")
            .min(3, "Username must be at least 3 characters")
            .max(50, "Username must be at most 50 characters"),
        password: z
            .string()
            .min(1, "Password is required")
            .min(8, "Password must be at least 8 characters")
            .max(128, "Password must be at most 128 characters"),
        confirm_password: z.string().min(1, "Confirm password is required"),
    })
    .superRefine((values, context) => {
        if (countPasswordClasses(values.password) < 3) {
            context.addIssue({
                code: "custom",
                message: "Password must contain at least 3 character types",
                path: ["password"],
            });
        }

        const username = values.username.trim().toLowerCase();
        if (
            username.length > 0 &&
            values.password.toLowerCase().includes(username)
        ) {
            context.addIssue({
                code: "custom",
                message: "Password must not contain your username",
                path: ["password"],
            });
        }

        if (values.confirm_password !== values.password) {
            context.addIssue({
                code: "custom",
                message: "Passwords do not match",
                path: ["confirm_password"],
            });
        }
    });

type SetupFormValues = z.infer<typeof setupSchema>;

function getSetupErrorMessage(error: unknown): string {
    if (axios.isAxiosError<ApiErrorResponse>(error)) {
        const message = error.response?.data?.error?.message;

        if (message) {
            return message;
        }
    }

    return "Unable to create account. Check your connection and try again.";
}

function SetupPage() {
    const navigate = useNavigate();
    const markSetupComplete = useAuthStore((state) => state.markSetupComplete);
    const [showPassword, setShowPassword] = useState(false);
    const [showConfirmPassword, setShowConfirmPassword] = useState(false);
    const [requestError, setRequestError] = useState<string | null>(null);
    const {
        control,
        formState: { errors, isSubmitting },
        handleSubmit,
        register,
    } = useForm<SetupFormValues>({
        resolver: zodResolver(setupSchema),
        defaultValues: {
            username: "",
            password: "",
            confirm_password: "",
        },
    });

    const username = useWatch({ control, name: "username" });
    const password = useWatch({ control, name: "password" });
    const passwordRequirements = [
        {
            label: "8–128 characters",
            valid: password.length >= 8 && password.length <= 128,
        },
        {
            label: "At least 3 of: uppercase, lowercase, number, special character",
            valid: countPasswordClasses(password) >= 3,
        },
        {
            label: "Does not contain your username",
            valid:
                username.trim().length > 0 &&
                password.length > 0 &&
                !password.toLowerCase().includes(username.trim().toLowerCase()),
        },
    ];

    const onSubmit = handleSubmit(async (values) => {
        setRequestError(null);

        try {
            await setup(values);
            markSetupComplete();
            navigate("/login", { replace: true });
        } catch (error) {
            setRequestError(getSetupErrorMessage(error));
        }
    });

    return (
        <main className="login-page setup-page">
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
                <div className="login-form-shell setup-form-shell">
                    <header className="login-heading">
                        <h1>Set up your account</h1>
                        <p>Create the administrator account for this device.</p>
                    </header>

                    <form className="login-form setup-form" onSubmit={onSubmit} noValidate>
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
                                    autoComplete="new-password"
                                    aria-invalid={Boolean(errors.password)}
                                    aria-describedby={
                                        errors.password
                                            ? "password-error password-requirements"
                                            : "password-requirements"
                                    }
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

                        <div className="login-field">
                            <label htmlFor="confirm-password">Confirm password</label>
                            <div className="login-password-control">
                                <input
                                    id="confirm-password"
                                    type={showConfirmPassword ? "text" : "password"}
                                    autoComplete="new-password"
                                    aria-invalid={Boolean(errors.confirm_password)}
                                    aria-describedby={
                                        errors.confirm_password ? "confirm-password-error" : undefined
                                    }
                                    {...register("confirm_password")}
                                />
                                <button
                                    type="button"
                                    className="login-password-toggle"
                                    onClick={() =>
                                        setShowConfirmPassword((current) => !current)
                                    }
                                    aria-label={
                                        showConfirmPassword
                                            ? "Hide confirm password"
                                            : "Show confirm password"
                                    }
                                    aria-pressed={showConfirmPassword}
                                >
                                    {showConfirmPassword ? (
                                        <EyeOff aria-hidden="true" size={22} />
                                    ) : (
                                        <Eye aria-hidden="true" size={22} />
                                    )}
                                </button>
                            </div>
                            {errors.confirm_password && (
                                <p id="confirm-password-error" className="login-field-error">
                                    {errors.confirm_password.message}
                                </p>
                            )}
                        </div>

                        <section
                            id="password-requirements"
                            className="setup-requirements"
                            aria-labelledby="password-requirements-heading"
                        >
                            <h2 id="password-requirements-heading">Password requirements</h2>
                            <ul>
                                {passwordRequirements.map((requirement) => (
                                    <li
                                        key={requirement.label}
                                        className={requirement.valid ? "is-valid" : undefined}
                                    >
                                        {requirement.valid ? (
                                            <CircleCheck aria-hidden="true" size={19} />
                                        ) : (
                                            <Circle aria-hidden="true" size={19} />
                                        )}
                                        <span>{requirement.label}</span>
                                    </li>
                                ))}
                            </ul>
                        </section>

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
                                    Creating account…
                                </>
                            ) : (
                                "Create account"
                            )}
                        </button>
                    </form>
                </div>
            </section>
        </main>
    );
}

export default SetupPage;
