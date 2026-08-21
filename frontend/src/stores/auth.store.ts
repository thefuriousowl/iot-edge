import { create } from "zustand";

import {
  clearAccessToken as clearApiAccessToken,
  setAccessToken as setApiAccessToken,
} from "../services/api";
import type { AuthUser } from "../types/auth";

export interface AuthState {
  user: AuthUser | null;
  accessToken: string | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  setSession: (user: AuthUser, accessToken: string) => void;
  setAccessToken: (accessToken: string) => void;
  setLoading: (isLoading: boolean) => void;
  clearSession: () => void;
}

const initialState = {
  user: null,
  accessToken: null,
  isAuthenticated: false,
  isLoading: false,
} satisfies Pick<
  AuthState,
  "user" | "accessToken" | "isAuthenticated" | "isLoading"
>;

export const useAuthStore = create<AuthState>((set) => ({
  ...initialState,

  setSession: (user, accessToken) => {
    setApiAccessToken(accessToken);
    set({
      user,
      accessToken,
      isAuthenticated: true,
      isLoading: false,
    });
  },

  setAccessToken: (accessToken) => {
    setApiAccessToken(accessToken);
    set({ accessToken });
  },

  setLoading: (isLoading) => {
    set({ isLoading });
  },

  clearSession: () => {
    clearApiAccessToken();
    set(initialState);
  },
}));
