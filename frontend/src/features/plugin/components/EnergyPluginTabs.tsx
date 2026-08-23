import { BarChart3, Gauge } from "lucide-react";
import { NavLink } from "react-router-dom";

import "./EnergyPluginTabs.css";

function EnergyPluginTabs({ instanceID }: { instanceID: string }) {
  const base = `/plugins/${encodeURIComponent(instanceID)}/energy`;
  return (
    <nav className="energy-plugin-tabs" aria-label="Energy Plugin views">
      <NavLink end to={base}><Gauge aria-hidden="true" size={16} />Overview</NavLink>
      <NavLink to={`${base}/history`}><BarChart3 aria-hidden="true" size={16} />History & charts</NavLink>
    </nav>
  );
}

export default EnergyPluginTabs;
