export type NavItem = {
  path: string;
  label: string;
  short: string;
  subtitle: string;
};

export const navItems: NavItem[] = [
  { path: "/dashboard", label: "Dashboard", short: "DB", subtitle: "Service health, throughput snapshot, and runtime counts." },
  { path: "/messages", label: "Messages", short: "MS", subtitle: "Message state inspection and operator status overrides." },
  { path: "/peers", label: "Peers", short: "PR", subtitle: "MM4 peers and transport route definitions." },
  { path: "/mm3", label: "MM3", short: "M3", subtitle: "Email relay controls and MM3 sender normalization." },
  { path: "/vasps", label: "VASPs", short: "V7", subtitle: "MM7 credentials, protocol mode, callbacks, and sender controls." },
  { path: "/adaptation", label: "Adaptation", short: "AD", subtitle: "Media adaptation classes and delivery constraints." },
  { path: "/oam", label: "OAM", short: "OA", subtitle: "Operations, queue visibility, and service-level status." },
];
