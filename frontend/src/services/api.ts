import axios from "axios";

let accessToken: string | null = null;

export function setAccessToken(token: string): void {
  accessToken = token;
}

export function clearAccessToken(): void {
  accessToken = null;
}

const api = axios.create({
  baseURL: import.meta.env.VITE_API_URL,
  headers: {
    "Content-Type": "application/json",
  },
  withCredentials: true
});

api.interceptors.request.use(
  (requestConfig) => {
    if (accessToken) {
      requestConfig.headers.set("Authorization", `Bearer ${accessToken}`);
    }

    return requestConfig;
  },
  (error: unknown) => Promise.reject(error),
);

api.interceptors.response.use(
  (response) => response,
  (error: unknown) => Promise.reject(error),
);

export default api;
