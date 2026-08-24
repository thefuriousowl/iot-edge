import { type ReactNode, useEffect, useRef, useState } from "react";
import {
  Activity,
  Boxes,
  ChevronRight,
  Cpu,
  DatabaseZap,
  FileChartColumn,
  LayoutDashboard,
  KeyRound,
  Menu,
  Puzzle,
  RadioTower,
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
  const [compactNavigation, setCompactNavigation] = useState(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const restoreMenuFocusRef = useRef(false);

  useEffect(() => {
    if (!window.matchMedia) return;
    const media = window.matchMedia("(max-width: 980px)");
    const synchronize = () => setCompactNavigation(media.matches);
    synchronize();
    media.addEventListener("change", synchronize);
    return () => media.removeEventListener("change", synchronize);
  }, []);

  useEffect(() => {
    if (navigationOpen) {
      closeButtonRef.current?.focus();
      const closeOnEscape = (event: KeyboardEvent) => {
        if (event.key === "Escape") setNavigationOpen(false);
      };
      document.addEventListener("keydown", closeOnEscape);
      return () => document.removeEventListener("keydown", closeOnEscape);
    }

    if (restoreMenuFocusRef.current) {
      menuButtonRef.current?.focus();
      restoreMenuFocusRef.current = false;
    }
  }, [navigationOpen]);

  function openNavigation() {
    restoreMenuFocusRef.current = true;
    setNavigationOpen(true);
  }

  return (
    <div className="vgateway-page">
      <button
        className={navigationOpen ? "vgateway-nav-backdrop is-open" : "vgateway-nav-backdrop"}
        type="button"
        aria-label="Close navigation"
        onClick={() => setNavigationOpen(false)}
      />

      <aside
        id="primary-navigation-drawer"
        className={navigationOpen ? "vgateway-sidebar is-open" : "vgateway-sidebar"}
        aria-hidden={compactNavigation && !navigationOpen ? true : undefined}
        inert={compactNavigation && !navigationOpen}
      >
        <div className="vgateway-brand">
          <Activity aria-hidden="true" strokeWidth={2.25} />
          <span>IoT Edge</span>
          <button
            ref={closeButtonRef}
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
          <NavLink to="/data-publishers">
            <RadioTower aria-hidden="true" size={21} />
            Data Publishers
          </NavLink>
          <NavLink to="/credentials">
            <KeyRound aria-hidden="true" size={21} />
            Credentials
          </NavLink>
          <NavLink to="/settings">
            <Settings aria-hidden="true" size={21} />
            Settings
          </NavLink>
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
            ref={menuButtonRef}
            type="button"
            className="vgateway-menu-button"
            aria-label="Open navigation"
            aria-controls="primary-navigation-drawer"
            aria-expanded={navigationOpen}
            onClick={openNavigation}
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
