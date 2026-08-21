import api from "./api";

import type {
    LoginRequest,
    LoginResponse,
    SetupRequest,
    SetupResponse,
    SetupStatusResponse,
    AuthUser,
    LogoutResponse,
    RefreshResponse,
} from "../types/auth";


export async function checkSetupStatus(): Promise<SetupStatusResponse> {
    const response = await api.get<SetupStatusResponse>("/auth/setup/status");

    return response.data;
}

export async function setup(data: SetupRequest): Promise<SetupResponse> {
    const response = await api.post<SetupResponse>(
        "/auth/setup",
        data
    );

    return response.data;
}

export async function login(data: LoginRequest): Promise<LoginResponse> {
    const response = await api.post<LoginResponse>(
        "/auth/login",
        data
    );

    return response.data;
}

export async function refreshAccessToken(): Promise<RefreshResponse> {
    const response = await api.post<RefreshResponse>(
        "/auth/refresh",
    );

    return response.data
}

export async function logout(): Promise<LogoutResponse> {
    const response = await api.post<LogoutResponse>(
        "/auth/logout",
    );

    return response.data
}

export async function getMe(): Promise<AuthUser> {
    const response = await api.get<AuthUser>(
        "/auth/me",
    );

    return response.data
}
