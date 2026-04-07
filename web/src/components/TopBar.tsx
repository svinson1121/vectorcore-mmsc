import { useTheme } from "../theme";

export function TopBar({
  title,
  subtitle,
  connected,
}: {
  title: string;
  subtitle: string;
  connected: boolean;
}) {
  const { theme, toggleTheme } = useTheme();

  return (
    <header className="topbar">
      <div>
        <div className="topbar-title">{title}</div>
        <div className="page-subtitle">{subtitle}</div>
      </div>

      <div className="topbar-right">
        <div className="connection-indicator">
          <div className={`connection-dot ${connected ? "connected" : "error"}`} />
          <span>{connected ? "Connected" : "Degraded"}</span>
        </div>

        <button className="btn btn-ghost" type="button" onClick={toggleTheme}>
          {theme === "dark" ? "Light" : "Dark"}
        </button>
      </div>
    </header>
  );
}
