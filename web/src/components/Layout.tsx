import { Outlet, useLocation } from "react-router-dom";

import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";
import { useAPI } from "../lib/api";
import { navItems } from "../lib/navigation";

export function Layout() {
  const location = useLocation();
  const health = useAPI<{ status: string }>("/healthz", { status: "down" }, 15000);
  const selected = navItems.find((item) => item.path === location.pathname) || navItems[0];

  return (
    <div className="layout">
      <Sidebar items={navItems} />
      <div className="main-content">
        <TopBar title={selected.label} subtitle={selected.subtitle} connected={health.data.status === "ok" && !health.error} />
        <main className="page">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
