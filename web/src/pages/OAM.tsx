import { useMemo, useState } from "react";

import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { asArray, directionLabel, Message, originLabel, RuntimeSnapshot, sendRequest, SMPPStatus, smppStateLabel, statusLabel, SystemStatus, useAPI } from "../lib/api";

export function OAM() {
  const toast = useToast();
  const system = useAPI<SystemStatus>(
    "/api/v1/system/status",
    { version: "", uptime: "", started_at: "", queue_visible: 0, message_counts: {} },
    10000,
  );
  const messages = useAPI<{ messages: Message[] }>("/api/v1/messages?limit=200", { messages: [] }, 10000);
  const runtime = useAPI<RuntimeSnapshot>(
    "/api/v1/runtime",
    { peers: [], mm4_routes: [], vasps: [], mm3_relay: null, smpp_upstreams: [], adaptation: [] },
    10000,
  );
  const smpp = useAPI<{ upstreams: SMPPStatus[] }>("/api/v1/smpp/status", { upstreams: [] }, 10000);
  const [queueOpen, setQueueOpen] = useState(false);
  const [snapshotOpen, setSnapshotOpen] = useState(false);
  const [queueFilter, setQueueFilter] = useState<"all" | "queued" | "delivering">("all");
  const [queueSelected, setQueueSelected] = useState<Message | null>(null);
  const [busyID, setBusyID] = useState("");
  const [operatorError, setOperatorError] = useState("");

  const queueVisible = useMemo(
    () => asArray(messages.data.messages).filter((item) => item.Status === 0 || item.Status === 1),
    [messages.data.messages],
  );
  const filteredQueue = useMemo(() => {
    if (queueFilter === "queued") {
      return queueVisible.filter((item) => item.Status === 0);
    }
    if (queueFilter === "delivering") {
      return queueVisible.filter((item) => item.Status === 1);
    }
    return queueVisible;
  }, [queueFilter, queueVisible]);
  const peers = asArray(runtime.data.peers);
  const vasps = asArray(runtime.data.vasps);
  const upstreams = asArray(smpp.data.upstreams);
  const adaptation = asArray(runtime.data.adaptation);

  async function deleteQueuedMessage(id: string) {
    if (!window.confirm(`Delete queued message ${id}? This removes the message record and stored payload.`)) {
      return;
    }
    try {
      setBusyID(id);
      setOperatorError("");
      await sendRequest(`/api/v1/messages/${id}`, "DELETE");
      await Promise.all([system.reload(), messages.reload()]);
      if (queueSelected?.ID === id) {
        setQueueSelected(null);
      }
      toast.success("Queued message deleted", id);
    } catch (err) {
      const message = err instanceof Error ? err.message : "delete failed";
      setOperatorError(message);
      toast.error("Delete failed", message);
    } finally {
      setBusyID("");
    }
  }

  return (
    <div className="stack">
      {(operatorError || system.error || messages.error || runtime.error || smpp.error) && (
        <div className="notice error">{operatorError || system.error || messages.error || runtime.error || smpp.error}</div>
      )}

      <div className="page-actions">
        <div />
        <div className="toolbar">
          <button
            className="button secondary"
            type="button"
            onClick={() => void Promise.all([system.reload(), messages.reload(), runtime.reload(), smpp.reload()])}
          >
            Refresh
          </button>
        </div>
      </div>

      <div className="grid">
        <div className="section-shell" style={{ maxWidth: 520 }}>
          <div className="compact-head">
            <h3>System</h3>
          </div>
          <div className="table-wrap">
            <table>
              <tbody>
                <tr>
                  <td>
                    <strong>Version</strong>
                  </td>
                  <td className="mono">{system.data.version || "0.0.1d"}</td>
                </tr>
                <tr>
                  <td>
                    <strong>Uptime</strong>
                  </td>
                  <td className="mono">{system.data.uptime || "—"}</td>
                </tr>
                <tr>
                  <td>
                    <strong>Started</strong>
                  </td>
                  <td className="mono">{system.data.started_at || "—"}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div className="mt-16">
            <div className="page-subtitle">Queue visibility includes queued and delivering messages.</div>
            <div className="inline-actions">
              <button className="button" type="button" onClick={() => setQueueOpen(true)}>
                View Queue ({system.data.queue_visible || queueVisible.length})
              </button>
              <button className="btn btn-ghost" type="button" onClick={() => setSnapshotOpen(true)}>
                Runtime Snapshot
              </button>
            </div>
          </div>
        </div>

        <div className="section-shell">
          <div className="compact-head">
            <h3>Protocol Runtime</h3>
          </div>
          <div className="grid">
            <div className="card summary-card">
              <span className="pill">MM4</span>
              <strong>{peers.length}</strong>
              <div className="summary-card-copy">Peer routes loaded into active runtime.</div>
            </div>
            <div className="card summary-card">
              <span className="pill">MM7</span>
              <strong>{vasps.length}</strong>
              <div className="summary-card-copy">SOAP and EAIF VASPs currently active.</div>
            </div>
            <div className="card summary-card">
              <span className="pill">MM3</span>
              <strong>{runtime.data.mm3_relay?.Enabled ? "Enabled" : "Disabled"}</strong>
              <div className="summary-card-copy">Email relay state from mutable runtime config.</div>
            </div>
            <div className="card summary-card">
              <span className="pill">Adaptation</span>
              <strong>{adaptation.length}</strong>
              <div className="summary-card-copy">Constraint classes visible to the delivery pipeline.</div>
            </div>
          </div>
        </div>
      </div>

      <div className="section-shell">
        <div className="compact-head">
          <h3>SMPP Sessions</h3>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Endpoint</th>
                <th>State</th>
                <th>System ID</th>
              </tr>
            </thead>
            <tbody>
              {upstreams.length > 0 ? (
                upstreams.map((item) => (
                  <tr key={`${item.name}-${item.host}-${item.port}`}>
                    <td className="mono text-sm">{item.name}</td>
                    <td className="mono text-sm">
                      {item.host}:{item.port}
                    </td>
                    <td className="text-sm">{smppStateLabel(item.state)}</td>
                    <td className="mono text-sm">{item.system_id || "—"}</td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={4}>No SMPP sessions are currently visible.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {queueOpen ? (
        <Modal title="Queued Messages" onClose={() => setQueueOpen(false)} size="lg">
          <div className="modal-body surface-stack">
            <div className="toolbar">
              <select className="input" value={queueFilter} onChange={(event) => setQueueFilter(event.target.value as "all" | "queued" | "delivering")}>
                <option value="all">Queued + Delivering</option>
                <option value="queued">Queued Only</option>
                <option value="delivering">Delivering Only</option>
              </select>
              <div className="text-muted text-sm">Visible queue rows: {filteredQueue.length}</div>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Status</th>
                    <th>Flow</th>
                    <th>From</th>
                    <th>To</th>
                    <th>ID</th>
                    <th>Origin</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredQueue.length > 0 ? (
                    filteredQueue.map((item) => (
                      <tr key={item.ID}>
                        <td>{statusLabel(item.Status)}</td>
                        <td className="text-sm">{directionLabel(item.Direction)}</td>
                        <td className="mono">{item.From || "—"}</td>
                        <td className="mono">{asArray(item.To).join(", ") || "—"}</td>
                        <td className="mono">{item.ID}</td>
                        <td className="mono">{item.OriginHost || originLabel(item.Origin)}</td>
                        <td>
                          <div className="flex gap-8">
                            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setQueueSelected(item)}>
                              Inspect
                            </button>
                            <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === item.ID} onClick={() => void deleteQueuedMessage(item.ID)}>
                              Delete
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))
                  ) : (
                    <tr>
                      <td colSpan={7}>No queued or delivering messages are visible.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </Modal>
      ) : null}

      {snapshotOpen ? (
        <Modal title="Runtime Snapshot" onClose={() => setSnapshotOpen(false)} size="lg">
          <div className="modal-body">
            <pre className="code">{JSON.stringify(runtime.data, null, 2)}</pre>
          </div>
        </Modal>
      ) : null}

      {queueSelected ? (
        <Modal title={`Queue Message ${queueSelected.ID}`} onClose={() => setQueueSelected(null)} size="lg">
          <div className="modal-body surface-stack">
            <div className="grid">
              <div className="card summary-card">
                <span className="pill">Status</span>
                <strong>{statusLabel(queueSelected.Status)}</strong>
                <div className="summary-card-copy">Current queue-facing lifecycle state.</div>
              </div>
              <div className="card summary-card">
                <span className="pill">Flow</span>
                <strong>{directionLabel(queueSelected.Direction)} / {originLabel(queueSelected.Origin)}</strong>
                <div className="summary-card-copy">Direction and ingress interface for the queue record.</div>
              </div>
              <div className="card summary-card">
                <span className="pill">Payload</span>
                <strong>{queueSelected.MessageSize || 0} B</strong>
                <div className="summary-card-copy">Stored payload size visible from the current queue slice.</div>
              </div>
            </div>
            <div className="section-shell">
              <h2>Addressing</h2>
              <div className="surface-grid">
                <div className="field">
                  <span>From</span>
                  <div className="mono text-sm">{queueSelected.From || "—"}</div>
                </div>
                <div className="field">
                  <span>To</span>
                  <div className="mono text-sm">{asArray(queueSelected.To).join(", ") || "—"}</div>
                </div>
                <div className="field">
                  <span>Origin Host</span>
                  <div className="mono text-sm">{queueSelected.OriginHost || "local"}</div>
                </div>
                <div className="field">
                  <span>Updated</span>
                  <div className="mono text-sm">{queueSelected.UpdatedAt || "—"}</div>
                </div>
              </div>
            </div>
            <div className="section-shell">
              <h2>Operator Read</h2>
              <div className="surface-note">{queueOperatorSummary(queueSelected)}</div>
              <div className="surface-note">{queueNextAction(queueSelected)}</div>
            </div>
            <div className="surface-note">
              Queue inspection here is intentionally lightweight. Use the Messages page for full event history, SMPP trail, and operator status overrides.
            </div>
            <div className="inline-actions">
              <button className="btn btn-ghost btn-sm" type="button" disabled={busyID === queueSelected.ID} onClick={() => void deleteQueuedMessage(queueSelected.ID)}>
                Delete Queued Message
              </button>
            </div>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}

function queueOperatorSummary(item: Message): string {
  if (item.Status === 1) {
    return `This message is actively delivering. The visible queue slice still contains it, which usually means a transport attempt is in flight or waiting on downstream completion.`;
  }
  if (item.Origin === 1) {
    return `This queued record is on an MM4-origin path and may still be waiting on inter-MMSC handling or local delivery continuation.`;
  }
  if (item.Origin === 3) {
    return `This queued record is on an MM7-origin path and may still be waiting on handset notification or downstream callback conditions.`;
  }
  if (item.Origin === 2) {
    return `This queued record is on the MM3 email path and may still be waiting on local handset delivery after inbound mail normalization.`;
  }
  return `This queued record is visible in the active queue slice with lifecycle state ${statusLabel(item.Status)}.`;
}

function queueNextAction(item: Message): string {
  if (item.Status === 1) {
    return "Recommended operator next step: watch briefly before intervening, then move to the Messages page if the record remains stuck without new delivery evidence.";
  }
  return "Recommended operator next step: use the Messages page for event history and transport trail before forcing a manual lifecycle change.";
}
