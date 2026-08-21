export interface AuthUser {
  id: string;
  username: string;
  created_at: string;
  last_login: string | null;
}
export interface SetupStatusResponse {
  setup_required: boolean;
}

export interface SetupRequest {
  username: string;
  password: string;
  confirm_password: string;
}

export interface SetupResponse {
  message: string;
  user: Pick<AuthUser, "id" | "username" | "created_at">;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface LoginResponse {
  access_token: string;
  token_type: string;
  expires_in: number;
}

export interface RefreshResponse {
  access_token: string;
  expires_in: number;
}

export interface LogoutResponse {
  message: string;
}

export interface ApiErrorResponse {
  error: {
    code: string;
    message: string;
  };
}
