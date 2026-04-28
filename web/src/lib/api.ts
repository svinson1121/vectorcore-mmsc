import { startTransition, useEffect, useState } from "react";

export interface Message {
  ID: string;
  TransactionID: string;
  Status: number;
  Direction: number;
  From: string;
  To: string[];
  Subject: string;
  ContentType?: string;
  MMSVersion?: string;
  DeliveryReport?: boolean;
  ReadReport?: boolean;
  MessageSize: number;
  ContentPath?: string;
  StoreID?: string;
  Origin: number;
  OriginHost: string;
  ReceivedAt?: string;
  UpdatedAt: string;
  Expiry?: string | null;
}

export interface MessageEvent {
  ID: number;
  MessageID: string;
  Source: string;
  Type: string;
  Summary: string;
  Detail: string;
  CreatedAt?: string | null;
}

export interface Peer {
  Name: string;
  Domain: string;
  SMTPHost: string;
  SMTPPort: number;
  SMTPAuth: boolean;
  SMTPUser: string;
  SMTPPass: string;
  TLSEnabled: boolean;
  AllowedIPs: string[];
  Active: boolean;
}

export interface MM4Route {
  ID: number;
  Name: string;
  MatchType: string;
  MatchValue: string;
  EgressType: string;
  EgressTarget: string;
  EgressPeerDomain: string;
  Priority: number;
  Active: boolean;
}

export interface VASP {
  VASPID: string;
  VASID: string;
  Protocol: string;
  Version: string;
  SharedSecret: string;
  AllowedIPs: string[];
  DeliverURL: string;
  ReportURL: string;
  MaxMsgSize: number;
  Active: boolean;
}

export interface MM3Relay {
  Enabled: boolean;
  SMTPHost: string;
  SMTPPort: number;
  SMTPAuth: boolean;
  SMTPUser: string;
  SMTPPass: string;
  TLSEnabled: boolean;
  DefaultSenderDomain: string;
  DefaultFromAddress: string;
}

export interface AdaptationClass {
  Name: string;
  MaxMsgSizeBytes: number;
  MaxImageWidth: number;
  MaxImageHeight: number;
  AllowedImageTypes: string[];
  AllowedAudioTypes: string[];
  AllowedVideoTypes: string[];
}

export interface SMPPUpstream {
  Name: string;
  Host: string;
  Port: number;
  SystemID: string;
  Password: string;
  SystemType: string;
  BindMode: string;
  EnquireLink: number;
  ReconnectWait: number;
  Active: boolean;
}

export interface SMPPStatus {
  name: string;
  host: string;
  port: number;
  state: number;
  system_id: string;
}

export interface SMPPSubmission {
  UpstreamName: string;
  SMPPMessageID: string;
  InternalMessageID: string;
  Recipient: string;
  SegmentIndex: number;
  SegmentCount: number;
  State: number;
  ErrorText: string;
  SubmittedAt?: string | null;
  CompletedAt?: string | null;
}

export function smppSubmissionStateLabel(state: number): string {
  switch (state) {
    case 0:
      return "Pending";
    case 1:
      return "Delivered";
    case 2:
      return "Failed";
    default:
      return `State ${state}`;
  }
}

export interface RuntimeSnapshot {
  peers: Peer[];
  mm4_routes: MM4Route[];
  mm3_relay?: MM3Relay | null;
  vasps: VASP[];
  smpp_upstreams: SMPPUpstream[];
  adaptation: AdaptationClass[];
}

export interface SystemStatus {
  version: string;
  uptime: string;
  started_at: string;
  queue_visible: number;
  message_counts: Record<string, number>;
}

export interface SystemConfig {
  adapt_enabled: boolean;
  max_message_size_bytes: number;
}

export interface APIState<T> {
  data: T;
  loading: boolean;
  error: string;
  reload: () => Promise<void>;
}

export async function fetchJSON<T>(path: string): Promise<T> {
  const response = await fetch(path, {
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  const text = await response.text();
  if (!text.trim()) {
    return undefined as T;
  }
  const payload = JSON.parse(text) as Record<string, unknown>;
  if (payload && typeof payload === "object" && "body" in payload) {
    return payload.body as T;
  }
  return payload as T;
}

export async function sendJSON<T>(path: string, method: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  const text = await response.text();
  if (!text.trim()) {
    return undefined as T;
  }
  const payload = JSON.parse(text) as Record<string, unknown>;
  if (payload && typeof payload === "object" && "body" in payload) {
    return payload.body as T;
  }
  return payload as T;
}

export async function sendRequest(path: string, method: string): Promise<void> {
  const response = await fetch(path, {
    method,
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
}

export function useAPI<T>(path: string, initial: T, refreshMs = 0): APIState<T> {
  const [data, setData] = useState<T>(initial);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  async function load() {
    try {
      setLoading(true);
      const next = await fetchJSON<T>(path);
      startTransition(() => {
        setData(next);
        setError("");
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : "request failed");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
    if (refreshMs <= 0) {
      return;
    }
    const timer = window.setInterval(() => {
      void load();
    }, refreshMs);
    return () => window.clearInterval(timer);
  }, [path, refreshMs]);

  return { data, loading, error, reload: load };
}

export function statusLabel(status: number): string {
  switch (status) {
    case 0:
      return "Queued";
    case 1:
      return "Delivering";
    case 2:
      return "Delivered";
    case 3:
      return "Expired";
    case 4:
      return "Rejected";
    case 5:
      return "Forwarded";
    case 6:
      return "Unreachable";
    default:
      return `Status ${status}`;
  }
}

export function directionLabel(direction: number): string {
  switch (direction) {
    case 0:
      return "MO";
    case 1:
      return "MT";
    default:
      return `Direction ${direction}`;
  }
}

export function originLabel(origin: number): string {
  switch (origin) {
    case 0:
      return "MM1";
    case 1:
      return "MM4";
    case 2:
      return "MM3";
    case 3:
      return "MM7";
    default:
      return `IF ${origin}`;
  }
}

export function smppStateLabel(state: number): string {
  switch (state) {
    case 0:
      return "Disconnected";
    case 1:
      return "Connecting";
    case 2:
      return "Bound";
    default:
      return `State ${state}`;
  }
}

export function formatBytes(value: number): string {
  if (!value) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit++;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

export function csv(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function asArray<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}
