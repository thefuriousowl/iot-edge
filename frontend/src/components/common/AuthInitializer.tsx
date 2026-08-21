import { useEffect } from "react";

import { useAuthStore } from "../../stores/auth.store";

function AuthInitializer() {
  const initializeSession = useAuthStore((state) => state.initializeSession);

  useEffect(() => {
    void initializeSession();
  }, [initializeSession]);

  return null;
}

export default AuthInitializer;
