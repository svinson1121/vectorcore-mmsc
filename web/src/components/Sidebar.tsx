import { NavLink } from "react-router-dom";

import type { NavItem } from "../lib/navigation";

export function Sidebar({
  items,
}: {
  items: NavItem[];
}) {
  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <div className="sidebar-logo">VectorCore</div>
        <div className="sidebar-logo-sub">MMS CENTER</div>
      </div>

      <nav className="sidebar-nav" aria-label="Primary navigation">
        {items.map((item) => (
          <NavLink
            key={item.path}
            to={item.path}
            className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
          >
            {item.label}
          </NavLink>
        ))}
      </nav>

      <div className="sidebar-footer">
      </div>
    </aside>
  );
}
