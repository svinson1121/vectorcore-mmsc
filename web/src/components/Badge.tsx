import { PropsWithChildren } from "react";

export function Badge({ tone = "info", children }: PropsWithChildren<{ tone?: "info" | "success" | "warning" | "danger" | "muted" }>) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}
