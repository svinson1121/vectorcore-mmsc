import { PropsWithChildren } from "react";

export function StatCard({
  title,
  value,
  subtitle,
  tooltip,
  tone = "accent",
  children,
}: PropsWithChildren<{
  title: string;
  value: string | number;
  subtitle?: string;
  tooltip?: string;
  tone?: "accent" | "success" | "warning" | "danger";
}>) {
  return (
    <div className="stat-card" title={tooltip || subtitle || title}>
      <div className={`stat-card-icon tone-${tone}`}>{title.slice(0, 2).toUpperCase()}</div>
      <div className="stat-card-body">
        <div className={`stat-card-value tone-${tone}`}>{value}</div>
        <div className="stat-card-label">{title}</div>
        {subtitle && <div className="stat-card-subtitle">{subtitle}</div>}
        {children}
      </div>
    </div>
  );
}
