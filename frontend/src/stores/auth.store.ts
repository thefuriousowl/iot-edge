import { create } from "zustand";

import {
  clearAccessToken as clearApiAccessToken,
  configureAuthSessionHandlers,
  setAccessToken as setApiAccessToken,
} from "../services/api";
import {
  checkSetupStatus,
  getMe,
  refreshAccessToken,
} from "../services/auth.service";
import type { AuthUser } from "../types/auth";

export interface AuthState {
  user: AuthUser | null;
  accessToken: string | null;
  isAuthenticated: boolean;
  isInitialized: boolean;
  isLoading: boolean;
  setupRequired: boolean | null;
  initializeSession: () => Promise<void>;
  markSetupComplete: () => void;
  setSession: (
    user: AuthUser,
    accessToken: string,
    expiresIn?: number,
  ) => void;
  setAccessToken: (accessToken: string, expiresIn?: number) => void;
  setLoading: (isLoading: boolean) => void;
  clearSession: () => void;
}

const initialState = {
  user: null,
  accessToken: null,
  isAuthenticated: false,
  isInitialized: false,
  isLoading: true,
  setupRequired: null,
} satisfies Pick<
  AuthState,
  | "user"
  | "accessToken"
  | "isAuthenticated"
  | "isInitialized"
  | "isLoading"
  | "setupRequired"
>;

let refreshTimer: ReturnType<typeof setTimeout> | null = null;
let initializationPromise: Promise<void> | null = null;

function clearRefreshTimer(): void {
  if (refreshTimer) {
    clearTimeout(refreshTimer);
    refreshTimer = null;
  }
}

function scheduleAccessTokenRefresh(expiresIn: number): void {
  clearRefreshTimer();

  if (!Number.isFinite(expiresIn) || expiresIn <= 0) {
    return;
  }

  const lifetimeMilliseconds = expiresIn * 1000;
  const refreshLeadMilliseconds = Math.min(
    60_000,
    Math.max(1_000, lifetimeMilliseconds * 0.1),
  );
  const refreshDelay = Math.max(
    lifetimeMilliseconds - refreshLeadMilliseconds,
    0,
  );

  refreshTimer = setTimeout(() => {
    void (async () => {
      try {
        const response = await refreshAccessToken();
        useAuthStore
          .getState()
          .setAccessToken(response.access_token, response.expires_in);
      } catch {
        useAuthStore.getState().clearSession();
      }
    })();
  }, refreshDelay);
}

export const useAuthStore = create<AuthState>((set, get) => ({
  ...initialState,

  initializeSession: async () => {
    if (get().isInitialized) {
      return;
    }

    if (initializationPromise) {
      return initializationPromise;
    }

    set({ isLoading: true });

    const pendingInitialization = (async () => {
      try {
        const setupStatus = await checkSetupStatus();

        if (setupStatus.setup_required) {
          clearRefreshTimer();
          clearApiAccessToken();
          set({
            user: null,
            accessToken: null,
            isAuthenticated: false,
            isInitialized: true,
            isLoading: false,
            setupRequired: true,
          });
          return;
        }

        set({ setupRequired: false });
      } catch {
        set({ setupRequired: null });
      }

      try {
        const tokenResponse = await refreshAccessToken();
        setApiAccessToken(tokenResponse.access_token);

        const user = await getMe();
        get().setSession(
          user,
          tokenResponse.access_token,
          tokenResponse.expires_in,
        );
      } catch {
        get().clearSession();
      }
    })();

    initializationPromise = pendingInitialization;

    try {
      await pendingInitialization;
    } finally {
      if (initializationPromise === pendingInitialization) {
        initializationPromise = null;
      }
    }
  },

  markSetupComplete: () => {
    set({ setupRequired: false });
  },

  setSession: (user, accessToken, expiresIn) => {
    setApiAccessToken(accessToken);
    if (expiresIn !== undefined) {
      scheduleAccessTokenRefresh(expiresIn);
    }
    set({
      user,
      accessToken,
      isAuthenticated: true,
      isInitialized: true,
      isLoading: false,
    });
  },

  setAccessToken: (accessToken, expiresIn) => {
    setApiAccessToken(accessToken);
    if (expiresIn !== undefined) {
      scheduleAccessTokenRefresh(expiresIn);
    }
    set({ accessToken });
  },

  setLoading: (isLoading) => {
    set({ isLoading });
  },

  clearSession: () => {
    clearRefreshTimer();
    clearApiAccessToken();
    set({
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isInitialized: true,
      isLoading: false,
    });
  },
}));

configureAuthSessionHandlers({
  onTokenRefreshed: (response) => {
    useAuthStore
      .getState()
      .setAccessToken(response.access_token, response.expires_in);
  },
  onSessionExpired: () => {
    useAuthStore.getState().clearSession();
  },
});
