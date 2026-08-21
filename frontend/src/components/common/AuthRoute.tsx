import type { PropsWithChildren } from "react";
import { Navigate } from "react-router-dom";

import { useAuthStore } from "../../stores/auth.store";
import SessionLoading from "./SessionLoading";

type AuthRouteMode = "entry" | "login" | "setup";

interface AuthRouteProps extends PropsWithChildren {
  mode: AuthRouteMode;
}

function AuthRoute({ children, mode }: AuthRouteProps) {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const isInitialized = useAuthStore((state) => state.isInitialized);
  const setupRequired = useAuthStore((state) => state.setupRequired);

  if (!isInitialized) {
    return <SessionLoading />;
  }

  if (setupRequired) {
    return mode === "setup" ? children : <Navigate to="/setup" replace />;
  }

  if (mode === "setup") {
    return <Navigate to={isAuthenticated ? "/dashboard" : "/login"} replace />;
  }

  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return mode === "login" ? children : <Navigate to="/login" replace />;
}

export default AuthRoute;
