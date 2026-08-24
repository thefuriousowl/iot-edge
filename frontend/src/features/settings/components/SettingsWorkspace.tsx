import { DatabaseZap, ShieldCheck } from "lucide-react";
import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";

import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "../pages/SettingsPage.css";

interface SettingsWorkspaceProps {
  section: "Data Management" | "Account";
  children: ReactNode;
}

function SettingsWorkspace({ section, children }: SettingsWorkspaceProps) {
  return (
    <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Settings <span>/</span> <strong>{section}</strong></>}>
      <div className="settings-content">
        <header className="settings-heading">
          <p>System control</p>
          <h1>Settings</h1>
          <span>Manage persisted history and your local administrator account from one protected workspace.</span>
        </header>

        <nav className="settings-tabs" aria-label="Settings sections">
          <NavLink to="/settings/data-management">
            <DatabaseZap aria-hidden="true" size={18} />
            <span><strong>Data Management</strong><small>Storage, retention, and history access</small></span>
          </NavLink>
          <NavLink to="/settings/account">
            <ShieldCheck aria-hidden="true" size={18} />
            <span><strong>Account</strong><small>Password and session security</small></span>
          </NavLink>
        </nav>

        {children}
      </div>
    </VGatewayShell>
  );
}

export default SettingsWorkspace;
