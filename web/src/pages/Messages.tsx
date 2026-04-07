import { useEffect } from "react";
import { useDeferredValue, useState } from "react";

import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { asArray, directionLabel, fetchJSON, formatBytes, Message, MessageEvent, originLabel, sendJSON, sendRequest, smppSubmissionStateLabel, SMPPSubmission, statusLabel, useAPI } from "../lib/api";

const statusOptions = ["queued", "delivering", "delivered", "expired", "rejected", "forwarded", "unreachable"];
const directionOptions = [
  { value: "all", label: "All directions" },
  { value: "MO", label: "MO" },
  { value: "MT", label: "MT" },
];
const originOptions = [
  { value: "all", label: "All origins" },
  { value: "MM1", label: "MM1" },
  { value: "MM3", label: "MM3" },
  { value: "MM4", label: "MM4" },
  { value: "MM7", label: "MM7" },
];

function isQueueVisibleStatus(status: number | string | null | undefined): boolean {
  const value = Number(status);
  return value === 0 || value === 1;
}

export function Messages() {
  const toast = useToast();
  const [query, setQuery] = useState("");
  const deferredQuery = useDeferredValue(query);
  const [statusFilter, setStatusFilter] = useState("all");
  const [directionFilter, setDirectionFilter] = useState("all");
  const [originFilter, setOriginFilter] = useState("all");
  const [error, setError] = useState("");
  const [busyID, setBusyID] = useState("");
  const [selected, setSelected] = useState<Message | null>(null);
  const [detail, setDetail] = useState<Message | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [events, setEvents] = useState<MessageEvent[]>([]);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [eventsError, setEventsError] = useState("");
  const [submissions, setSubmissions] = useState<SMPPSubmission[]>([]);
  const [submissionsLoading, setSubmissionsLoading] = useState(false);
  const [submissionsError, setSubmissionsError] = useState("");
  const [operatorNote, setOperatorNote] = useState("");
  const { data, loading, error: loadError, reload } = useAPI<{ messages: Message[] }>(
    "/api/v1/messages?limit=100",
    { messages: [] },
    15000,
  );
  const messages = asArray(data.messages);
  const queued = messages.filter((item) => Number(item.Status) === 0).length;
  const delivering = messages.filter((item) => item.Status === 1).length;
  const delivered = messages.filter((item) => item.Status === 2).length;
  const withSubject = messages.filter((item) => item.Subject).length;

  const filtered = messages.filter((item) => {
    const needle = deferredQuery.trim().toLowerCase();
    if (statusFilter !== "all" && statusLabel(item.Status).toLowerCase() !== statusFilter) {
      return false;
    }
    if (directionFilter !== "all" && directionLabel(item.Direction) !== directionFilter) {
      return false;
    }
    if (originFilter !== "all" && originLabel(item.Origin) !== originFilter) {
      return false;
    }
    if (!needle) {
      return true;
    }
    return [
      item.ID,
      item.From,
      asArray(item.To).join(","),
      item.Subject,
      originLabel(item.Origin),
      statusLabel(item.Status),
    ]
      .join(" ")
      .toLowerCase()
      .includes(needle);
  });
  const filteredQueue = filtered.filter((item) => {
    const status = Number(item.Status);
    return status === 0 || status === 1;
  }).length;
  const filteredDelivered = filtered.filter((item) => item.Status === 2).length;
  const filteredWithSubject = filtered.filter((item) => item.Subject).length;

  async function updateStatus(id: string, status: string) {
    try {
      setBusyID(id);
      setError("");
      await sendJSON(`/api/v1/messages/${id}/status`, "PATCH", { status });
      await reload();
      toast.success("Message updated", `${id} -> ${status}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "status update failed";
      setError(message);
      toast.error("Update failed", message);
    } finally {
      setBusyID("");
    }
  }

  async function deleteMessage(id: string) {
    try {
      setBusyID(id);
      setError("");
      await sendRequest(`/api/v1/messages/${id}`, "DELETE");
      await reload();
      if (selected?.ID === id) {
        setSelected(null);
      }
      toast.success("Queued message deleted", id);
    } catch (err) {
      const message = err instanceof Error ? err.message : "delete failed";
      setError(message);
      toast.error("Delete failed", message);
    } finally {
      setBusyID("");
    }
  }

  async function postAction(id: string, action: "note" | "requeue") {
    try {
      setBusyID(id);
      setError("");
      await sendJSON(`/api/v1/messages/${id}/actions`, "POST", {
        action,
        note: operatorNote.trim(),
      });
      await reload();
      if (selected?.ID === id) {
        const next = detail || selected;
        setSelected({ ...next, Status: action === "requeue" ? 0 : next.Status });
        setDetail({ ...next, Status: action === "requeue" ? 0 : next.Status });
      }
      const payload = await fetchJSON<{ events: MessageEvent[] }>(`/api/v1/messages/${encodeURIComponent(id)}/events`);
      setEvents(asArray(payload.events));
      if (action === "note") {
        setOperatorNote("");
        toast.success("Operator note added", id);
      } else {
        toast.success("Message requeued", id);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : "operator action failed";
      setError(message);
      toast.error("Action failed", message);
    } finally {
      setBusyID("");
    }
  }

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      setDetailError("");
      setDetailLoading(false);
      setEvents([]);
      setEventsError("");
      setEventsLoading(false);
      setSubmissions([]);
      setSubmissionsError("");
      setSubmissionsLoading(false);
      setOperatorNote("");
      return;
    }
    let cancelled = false;
    setDetailLoading(true);
    setDetailError("");
    void fetchJSON<Message>(`/api/v1/messages/${encodeURIComponent(selected.ID)}`)
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setDetail(payload);
      })
      .catch((err) => {
        if (cancelled) {
          return;
        }
        setDetailError(err instanceof Error ? err.message : "failed to load message detail");
      })
      .finally(() => {
        if (!cancelled) {
          setDetailLoading(false);
        }
      });

    setSubmissionsLoading(true);
    setSubmissionsError("");
    setEventsLoading(true);
    setEventsError("");
    void fetchJSON<{ submissions: SMPPSubmission[] }>(`/api/v1/messages/${encodeURIComponent(selected.ID)}/smpp-submissions`)
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setSubmissions(asArray(payload.submissions));
      })
      .catch((err) => {
        if (cancelled) {
          return;
        }
        setSubmissionsError(err instanceof Error ? err.message : "failed to load smpp submissions");
      })
      .finally(() => {
        if (!cancelled) {
          setSubmissionsLoading(false);
        }
      });
    void fetchJSON<{ events: MessageEvent[] }>(`/api/v1/messages/${encodeURIComponent(selected.ID)}/events`)
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setEvents(asArray(payload.events));
      })
      .catch((err) => {
        if (cancelled) {
          return;
        }
        setEventsError(err instanceof Error ? err.message : "failed to load message events");
      })
      .finally(() => {
        if (!cancelled) {
          setEventsLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [selected]);

  return (
    <div className="stack">
      <div className="grid">
        <div className="card summary-card">
          <span className="pill">Visible Messages</span>
          <strong>{filtered.length}</strong>
          <div className="summary-card-copy">Records matching the current operator filter.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Queue</span>
          <strong>{filteredQueue}</strong>
          <div className="summary-card-copy">Queued or actively delivering in the current view.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Delivered</span>
          <strong>{filteredDelivered}</strong>
          <div className="summary-card-copy">Final delivered state in the current view.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">With Subject</span>
          <strong>{filteredWithSubject}</strong>
          <div className="summary-card-copy">Records carrying a visible subject line.</div>
        </div>
      </div>

      <div className="toolbar">
        <input
          className="input"
          placeholder="Filter by ID, address, subject, or status"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <select className="input" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
          <option value="all">All statuses</option>
          {statusOptions.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
        <select className="input" value={directionFilter} onChange={(event) => setDirectionFilter(event.target.value)}>
          {directionOptions.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <select className="input" value={originFilter} onChange={(event) => setOriginFilter(event.target.value)}>
          {originOptions.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <button
          className="button secondary"
          type="button"
          onClick={() => {
            setQuery("");
            setStatusFilter("all");
            setDirectionFilter("all");
            setOriginFilter("all");
          }}
        >
          Clear
        </button>
        <button className="button secondary" type="button" onClick={() => void reload()}>
          Refresh
        </button>
      </div>

      {(error || loadError) && <div className="notice error">{error || loadError}</div>}
      {loading && messages.length === 0 ? (
        <div className="notice">Loading messages…</div>
      ) : filtered.length === 0 ? (
        <div className="notice">No messages matched the current filter.</div>
      ) : (
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Flow</th>
                <th>Addressing</th>
                <th>Subject</th>
                <th>Status</th>
                <th>Payload</th>
                <th>Received / Expiry</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((item) => (
                <tr key={item.ID}>
                  <td className="mono text-sm">{item.ID}</td>
                  <td>
                    <div style={{ display: "grid", gap: 4 }}>
                      <span className="text-sm">{directionLabel(item.Direction)}</span>
                      <span className="mono text-sm">{originLabel(item.Origin)}</span>
                    </div>
                  </td>
                  <td>
                    <div style={{ display: "grid", gap: 4 }}>
                      <span className="mono text-sm">{item.From || "—"}</span>
                      <span className="mono text-sm">{asArray(item.To).join(", ") || "—"}</span>
                    </div>
                  </td>
                  <td className="text-sm">{item.Subject || "No subject"}</td>
                  <td>
                    <span className="pill neutral">{statusLabel(item.Status)}</span>
                  </td>
                  <td>
                    <div style={{ display: "grid", gap: 4 }}>
                      <span className="mono text-sm">{formatBytes(item.MessageSize)}</span>
                      <span className="text-muted text-sm">{item.OriginHost || "local"}</span>
                    </div>
                  </td>
                  <td>
                    <div style={{ display: "grid", gap: 4 }}>
                      <span className="mono text-sm">{item.ReceivedAt ? formatShortTime(item.ReceivedAt) : "—"}</span>
                      <span className="mono text-sm text-muted">{item.Expiry ? formatShortTime(item.Expiry) : "no expiry"}</span>
                    </div>
                  </td>
                  <td>
                    <div className="flex gap-8">
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => setSelected(item)}>
                        Inspect
                      </button>
                      {isQueueVisibleStatus(item.Status) ? (
                        <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === item.ID} onClick={() => void deleteMessage(item.ID)}>
                          Delete
                        </button>
                      ) : null}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selected ? (
        <Modal title={`Message ${selected.ID}`} onClose={() => setSelected(null)} size="lg">
          <div className="modal-body surface-stack">
            {detailError ? <div className="notice error">{detailError}</div> : null}
            <div className="grid">
              <div className="card summary-card">
                <span className="pill">Status</span>
                <strong>{statusLabel((detail || selected).Status)}</strong>
                <div className="summary-card-copy">Current operator-visible lifecycle state.</div>
              </div>
              <div className="card summary-card">
                <span className="pill">Flow</span>
                <strong>{directionLabel((detail || selected).Direction)} / {originLabel((detail || selected).Origin)}</strong>
                <div className="summary-card-copy">Direction and ingress interface.</div>
              </div>
              <div className="card summary-card">
                <span className="pill">Payload</span>
                <strong>{formatBytes((detail || selected).MessageSize)}</strong>
                <div className="summary-card-copy">Stored payload size for this record.</div>
              </div>
            </div>

            <div className="section-shell">
              <h2>Addressing</h2>
              <div className="surface-grid">
                <div className="field">
                  <span>From</span>
                  <div className="mono text-sm">{(detail || selected).From || "—"}</div>
                </div>
                <div className="field">
                  <span>To</span>
                  <div className="mono text-sm">{asArray((detail || selected).To).join(", ") || "—"}</div>
                </div>
                <div className="field">
                  <span>Origin Host</span>
                  <div className="mono text-sm">{(detail || selected).OriginHost || "local"}</div>
                </div>
                <div className="field">
                  <span>Updated</span>
                  <div className="mono text-sm">{(detail || selected).UpdatedAt || "—"}</div>
                </div>
              </div>
            </div>

            <div className="section-shell">
              <h2>Message Metadata</h2>
              <div className="surface-grid">
                <div className="field">
                  <span>Transaction ID</span>
                  <div className="mono text-sm">{(detail || selected).TransactionID || "—"}</div>
                </div>
                <div className="field">
                  <span>Subject</span>
                  <div className="text-sm">{(detail || selected).Subject || "No subject"}</div>
                </div>
                <div className="field">
                  <span>Content Type</span>
                  <div className="mono text-sm">{(detail || selected).ContentType || "—"}</div>
                </div>
                <div className="field">
                  <span>MMS Version</span>
                  <div className="mono text-sm">{(detail || selected).MMSVersion || "—"}</div>
                </div>
              </div>
            </div>

            <div className="section-shell">
              <h2>Operator Summary</h2>
              <div className="surface-note">{operatorSummary(detail || selected, events, submissions)}</div>
              <div className="surface-note">{nextActionSummary(detail || selected, events, submissions)}</div>
            </div>

            <div className="section-shell">
              <div className="compact-head">
                <h2>Operator Actions</h2>
              </div>
              <div className="surface-grid">
                <label className="field" style={{ gridColumn: "1 / -1" }}>
                  <span>Operator Note</span>
                  <textarea
                    className="input"
                    rows={3}
                    placeholder="Add a note to the message event trail"
                    value={operatorNote}
                    onChange={(event) => setOperatorNote(event.target.value)}
                  />
                </label>
              </div>
              <div className="inline-actions">
                <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID || !operatorNote.trim()} onClick={() => void postAction(selected.ID, "note")}>
                  Add Note
                </button>
                {isQueueVisibleStatus((detail || selected).Status) ? (
                  <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID} onClick={() => void deleteMessage(selected.ID)}>
                    Delete
                  </button>
                ) : null}
                <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID} onClick={() => void postAction(selected.ID, "requeue")}>
                  Requeue
                </button>
                <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID} onClick={() => void updateStatus(selected.ID, "delivering")}>
                  Mark Delivering
                </button>
                <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID} onClick={() => void updateStatus(selected.ID, "delivered")}>
                  Mark Delivered
                </button>
                <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID} onClick={() => void updateStatus(selected.ID, "rejected")}>
                  Mark Rejected
                </button>
                <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === selected.ID} onClick={() => void updateStatus(selected.ID, "unreachable")}>
                  Mark Unreachable
                </button>
              </div>
            </div>

            <div className="section-shell">
              <h2>Message Event Trail</h2>
              {eventsError ? <div className="notice error">{eventsError}</div> : null}
              {eventsLoading ? (
                <div className="page-subtitle">Loading message events…</div>
              ) : events.length > 0 ? (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>When</th>
                        <th>Source</th>
                        <th>Event</th>
                        <th>Summary</th>
                        <th>Detail</th>
                      </tr>
                    </thead>
                    <tbody>
                      {events.map((item) => (
                        <tr key={item.ID}>
                          <td className="mono text-sm">{item.CreatedAt || "—"}</td>
                          <td className="mono text-sm">{item.Source}</td>
                          <td className="mono text-sm">{item.Type}</td>
                          <td className="text-sm">{item.Summary}</td>
                          <td className="mono text-sm">{item.Detail || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="page-subtitle">No message events are currently recorded for this message.</div>
              )}
            </div>

            <div className="section-shell">
              <h2>Inter-MMSC / VASP Trail</h2>
              {detailLoading ? <div className="page-subtitle">Loading message detail…</div> : null}
              <div className="surface-grid">
                <div className="field">
                  <span>Origin Interface</span>
                  <div className="mono text-sm">{originLabel((detail || selected).Origin)}</div>
                </div>
                <div className="field">
                  <span>Origin Target</span>
                  <div className="mono text-sm">{(detail || selected).OriginHost || "local"}</div>
                </div>
                <div className="field">
                  <span>Delivery Report Requested</span>
                  <div className="text-sm">{(detail || selected).DeliveryReport ? "Yes" : "No"}</div>
                </div>
                <div className="field">
                  <span>Read Report Requested</span>
                  <div className="text-sm">{(detail || selected).ReadReport ? "Yes" : "No"}</div>
                </div>
              </div>
              <div className="surface-note">
                {reportPathSummary(detail || selected)}
              </div>
            </div>

            <div className="section-shell">
              <h2>SMPP Delivery Trail</h2>
              {submissionsError ? <div className="notice error">{submissionsError}</div> : null}
              {submissionsLoading ? (
                <div className="page-subtitle">Loading SMPP submissions…</div>
              ) : submissions.length > 0 ? (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Upstream</th>
                        <th>Recipient</th>
                        <th>Segment</th>
                        <th>State</th>
                        <th>Remote ID</th>
                        <th>Error</th>
                      </tr>
                    </thead>
                    <tbody>
                      {submissions.map((item) => (
                        <tr key={`${item.UpstreamName}-${item.SMPPMessageID}-${item.SegmentIndex}`}>
                          <td className="mono text-sm">{item.UpstreamName}</td>
                          <td className="mono text-sm">{item.Recipient}</td>
                          <td className="mono text-sm">
                            {item.SegmentIndex + 1}/{item.SegmentCount}
                          </td>
                          <td className="text-sm">{smppSubmissionStateLabel(item.State)}</td>
                          <td className="mono text-sm">{item.SMPPMessageID}</td>
                          <td className="mono text-sm">{item.ErrorText || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="page-subtitle">No tracked SMPP submissions are associated with this message.</div>
              )}
            </div>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}

function formatShortTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function reportPathSummary(item: Message): string {
  if (item.Origin === 1 && item.OriginHost) {
    return `Inferred from stored metadata: MM4 delivery or read reports return toward origin peer ${item.OriginHost}. Current message status is ${statusLabel(item.Status)}.`;
  }
  if (item.Origin === 3 && item.OriginHost) {
    return `Inferred from stored metadata: MM7 callbacks target VASP ${item.OriginHost}. Delivery report requested: ${item.DeliveryReport ? "yes" : "no"}, read report requested: ${item.ReadReport ? "yes" : "no"}. Current message status is ${statusLabel(item.Status)}.`;
  }
  if (item.Origin === 2 && item.OriginHost) {
    return `MM3-originated traffic records the email ingress host as ${item.OriginHost}. The current message status is ${statusLabel(item.Status)}.`;
  }
  return `This message does not currently have a stored inter-MMSC or VASP callback trail beyond its current status of ${statusLabel(item.Status)}.`;
}

function operatorSummary(item: Message, events: MessageEvent[], submissions: SMPPSubmission[]): string {
  const latestEvent = events[0];
  const failedSubmission = submissions.find((entry) => entry.State === 2);
  const deliveredSubmission = submissions.find((entry) => entry.State === 1);

  if (failedSubmission) {
    return `Latest handset notification evidence shows SMPP failure on ${failedSubmission.UpstreamName}${failedSubmission.ErrorText ? ` with ${failedSubmission.ErrorText}` : ""}. Current lifecycle state is ${statusLabel(item.Status)}.`;
  }
  if (deliveredSubmission) {
    return `Latest handset notification evidence shows a delivered SMPP submission on ${deliveredSubmission.UpstreamName}. Current lifecycle state is ${statusLabel(item.Status)}.`;
  }
  if (latestEvent && latestEvent.Source === "operator") {
    return `Latest visible intervention is an operator ${latestEvent.Type} event: ${latestEvent.Summary}. Current lifecycle state is ${statusLabel(item.Status)}.`;
  }
  if (item.Origin === 1) {
    return `This record is on an inter-MMSC MM4 path. Current lifecycle state is ${statusLabel(item.Status)}, and origin metadata points to ${item.OriginHost || "the peer path"}.`;
  }
  if (item.Origin === 3) {
    return `This record is on an MM7 VASP path. Current lifecycle state is ${statusLabel(item.Status)}, and callback metadata points to ${item.OriginHost || "the configured VASP"}.`;
  }
  if (item.Origin === 2) {
    return `This record originated on the MM3 email path. Current lifecycle state is ${statusLabel(item.Status)}${item.OriginHost ? `, with ingress metadata from ${item.OriginHost}` : ""}.`;
  }
  return `This record is currently ${statusLabel(item.Status)} with no stronger protocol-specific evidence visible than the stored lifecycle and event trail.`;
}

function nextActionSummary(item: Message, events: MessageEvent[], submissions: SMPPSubmission[]): string {
  const latestEvent = events[0];
  const failedSubmission = submissions.find((entry) => entry.State === 2);
  const pendingSubmission = submissions.find((entry) => entry.State === 0);

  if (failedSubmission) {
    return "Recommended operator next step: inspect the failed submission cause, verify upstream reachability or addressing, then requeue only if the failure cause is cleared.";
  }
  if (pendingSubmission || item.Status === 1) {
    return "Recommended operator next step: monitor before intervening unless queue age or upstream state indicates the delivery attempt is stuck.";
  }
  if (item.Status === 0) {
    return "Recommended operator next step: inspect recent events for routing or transport gaps before forcing manual status changes.";
  }
  if (latestEvent && latestEvent.Source === "operator" && latestEvent.Type === "requeue") {
    return "Recommended operator next step: watch for a fresh transport event after the requeue request instead of stacking repeated manual actions.";
  }
  if (item.Status === 2) {
    return "Recommended operator next step: none unless callback or downstream report expectations remain open.";
  }
  return "Recommended operator next step: use the event trail and transport sections below to confirm whether this needs a note, a requeue, or no action.";
}
