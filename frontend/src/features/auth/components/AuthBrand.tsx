import { Activity } from "lucide-react";

import { APP_VERSION } from "../../../config/app";

function AuthBrand() {
  return (
    <div className="login-brand">
      <Activity aria-hidden="true" strokeWidth={2.25} />

      <div className="login-brand-text">
        <span>IoT Edge</span>
        <small className="login-brand-version">v{APP_VERSION}</small>
      </div>
    </div>
  );
}

export default AuthBrand;
