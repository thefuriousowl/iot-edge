import { type ReactNode, useState } from "react";
import {
  Activity,
  Boxes,
  ChevronRight,
  Cpu,
  DatabaseZap,
  FileChartColumn,
  LayoutDashboard,
  Menu,
  Puzzle,
  Settings,
  Tags,
  X,
} from "lucide-react";
import { Link, NavLink } from "react-router-dom";

import { useAuthStore } from "../../../stores/auth.store";
import InternetStatus from "../../system/components/InternetStatus";

interface VGatewayShellProps {
  breadcrumb: ReactNode;
  children: ReactNode;
}

function VGatewayShell({ breadcrumb, children }: VGatewayShellProps) {
  const username = useAuthStore((state) => state.user?.username ?? "Admin");
  const [navigationOpen, setNavigationOpen] = useState(false);

  return (
    <div className="vgateway-page">
      <button
        className={navigationOpen ? "vgateway-nav-backdrop is-open" : "vgateway-nav-backdrop"}
        type="button"
        aria-label="Close navigation"
        onClick={() => setNavigationOpen(false)}
      />

      <aside className={navigationOpen ? "vgateway-sidebar is-open" : "vgateway-sidebar"}>
        <div className="vgateway-brand">
          <Activity aria-hidden="true" strokeWidth={2.25} />
          <span>IoT Edge</span>
          <button
            type="button"
            className="vgateway-nav-close"
            aria-label="Close navigation"
            onClick={() => setNavigationOpen(false)}
          >
            <X aria-hidden="true" size={22} />
          </button>
        </div>

        <nav className="vgateway-navigation" aria-label="Primary navigation">
          <NavLink to="/dashboard">
            <LayoutDashboard aria-hidden="true" size={21} />
            Dashboard
          </NavLink>
          <NavLink to="/vgateways">
            <Cpu aria-hidden="true" size={21} />
            vGateways
          </NavLink>
          <NavLink to="/devices">
            <Boxes aria-hidden="true" size={21} />
            Devices
          </NavLink>
          <NavLink to="/tags">
            <Tags aria-hidden="true" size={21} />
            Tags
          </NavLink>
          <NavLink to="/data-loggers">
            <DatabaseZap aria-hidden="true" size={21} />
            Data Loggers
          </NavLink>
          <NavLink to="/reports">
            <FileChartColumn aria-hidden="true" size={21} />
            Reports
          </NavLink>
          <NavLink to="/plugins">
            <Puzzle aria-hidden="true" size={21} />
            Plugins
          </NavLink>
          <span aria-disabled="true">
            <Settings aria-hidden="true" size={21} />
            Settings
          </span>
        </nav>

        <Link className="vgateway-health-link" to="/dashboard">
          <span className="vgateway-health-dot" />
          <span>
            <strong>System health</strong>
            <small>Healthy</small>
          </span>
          <ChevronRight aria-hidden="true" size={18} />
        </Link>
      </aside>

      <main className="vgateway-workspace">
        <header className="vgateway-topbar">
          <button
            type="button"
            className="vgateway-menu-button"
            aria-label="Open navigation"
            onClick={() => setNavigationOpen(true)}
          >
            <Menu aria-hidden="true" size={23} />
          </button>
          <div className="vgateway-mobile-brand">
            <Activity aria-hidden="true" size={24} />
            <span>IoT Edge</span>
          </div>
          <p>{breadcrumb}</p>
          <div className="vgateway-topbar-actions">
            <InternetStatus />
            <div className="vgateway-user">
              <span>{username.slice(0, 1).toUpperCase()}</span>
              <strong>{username}</strong>
            </div>
          </div>
        </header>

        {children}
      </main>
    </div>
  );
}

export default VGatewayShell;
