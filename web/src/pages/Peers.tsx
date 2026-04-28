import { FormEvent, useState } from "react";

import { useToast } from "../components/Toast";
import { Modal } from "../components/Modal";
import { asArray, csv, Peer, sendJSON, sendRequest, SMPPStatus, SMPPUpstream, smppStateLabel, useAPI } from "../lib/api";

type PeerTab = "mm4" | "smpp";

export function Peers() {
  const [tab, setTab] = useState<PeerTab>("mm4");
  return (
    <div>
      <div className="tabs">
        <button className={`tab-btn${tab === "mm4" ? " active" : ""}`} type="button" onClick={() => setTab("mm4")}>
          MM4 Peers
        </button>
        <button className={`tab-btn${tab === "smpp" ? " active" : ""}`} type="button" onClick={() => setTab("smpp")}>
          SMPP peers
        </button>
      </div>
      {tab === "mm4" ? <MM4PeersTab /> : <SMPPUpstreamsTab />}
    </div>
  );
}

function MM4PeersTab() {
  const peers = useAPI<{ peers: Peer[] }>("/api/v1/peers", { peers: [] }, 15000);
  const toast = useToast();
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<Peer | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState({
    Name: "",
    Domain: "",
    SMTPHost: "",
    SMTPPort: "25",
    AllowedIPs: "",
    TLSEnabled: false,
    Active: true,
  });
  const peerList = asArray(peers.data.peers);
  const activePeers = peerList.filter((item) => item.Active).length;
  const tlsPeers = peerList.filter((item) => item.TLSEnabled).length;
  const allowlistedPeers = peerList.filter((item) => asArray(item.AllowedIPs).length > 0).length;

  function resetForm() {
    setForm({
      Name: "",
      Domain: "",
      SMTPHost: "",
      SMTPPort: "25",
      AllowedIPs: "",
      TLSEnabled: false,
      Active: true,
    });
  }

  function openAdd() {
    setEditing(null);
    setError("");
    resetForm();
    setShowAdd(true);
  }

  function openEdit(item: Peer) {
    setEditing(item);
    setError("");
    setForm({
      Name: item.Name || item.Domain,
      Domain: item.Domain,
      SMTPHost: item.SMTPHost,
      SMTPPort: String(item.SMTPPort),
      AllowedIPs: asArray(item.AllowedIPs).join(", "),
      TLSEnabled: item.TLSEnabled,
      Active: item.Active,
    });
    setShowAdd(true);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      setError("");
      const body = {
        Name: form.Name.trim() || form.Domain.trim(),
        Domain: form.Domain.trim(),
        SMTPHost: form.SMTPHost.trim(),
        SMTPPort: Number(form.SMTPPort) || 25,
        SMTPAuth: false,
        SMTPUser: "",
        SMTPPass: "",
        TLSEnabled: form.TLSEnabled,
        AllowedIPs: csv(form.AllowedIPs),
        Active: form.Active,
      };
      if (editing) {
        await sendJSON(`/api/v1/peers/${encodeURIComponent(editing.Domain)}`, "PUT", body);
        toast.success("Peer updated", editing.Domain);
      } else {
        await sendJSON("/api/v1/peers", "POST", body);
        toast.success("Peer created", form.Domain.trim());
      }
      resetForm();
      await peers.reload();
      setShowAdd(false);
      setEditing(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : "peer save failed";
      setError(message);
      toast.error("Save failed", message);
    }
  }

  async function removePeer(domain: string) {
    if (!window.confirm(`Delete peer ${domain}?`)) {
      return;
    }
    try {
      setError("");
      await sendRequest(`/api/v1/peers/${encodeURIComponent(domain)}`, "DELETE");
      await peers.reload();
      toast.success("Peer deleted", domain);
    } catch (err) {
      const message = err instanceof Error ? err.message : "peer delete failed";
      setError(message);
      toast.error("Delete failed", message);
    }
  }

  return (
    <div className="stack">
      <div className="grid">
        <div className="card summary-card">
          <span className="pill">Configured Peers</span>
          <strong>{peerList.length}</strong>
          <div className="summary-card-copy">MM4 domains currently defined in operator config.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Active</span>
          <strong>{activePeers}</strong>
          <div className="summary-card-copy">Peers eligible for relay and inbound authorization checks.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">TLS</span>
          <strong>{tlsPeers}</strong>
          <div className="summary-card-copy">Peers configured for encrypted SMTP transport.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">IP Controls</span>
          <strong>{allowlistedPeers}</strong>
          <div className="summary-card-copy">Peers with inbound source allowlists configured.</div>
        </div>
      </div>

      <div className="flex justify-between mb-12">
        <span className="text-muted text-sm">MM4 inter-MMSC transport endpoints</span>
        <div className="flex gap-8">
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => void peers.reload()}>
            Refresh
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={openAdd}>
            Add Peer
          </button>
        </div>
      </div>

      {(error || peers.error) && <div className="notice error">{error || peers.error}</div>}

      {peerList.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-title">No MM4 peers configured</div>
          <div className="text-muted text-sm">Add a peer to enable inter-carrier MM4 relay.</div>
          <button className="btn btn-primary mt-12" type="button" onClick={openAdd}>
            Add First Peer
          </button>
        </div>
      ) : (
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>Domain</th>
                <th>Name</th>
                <th>SMTP Host</th>
                <th>Port</th>
                <th>Transport</th>
                <th>Status</th>
                <th>Allowlist</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {peerList.map((item) => (
                <tr key={item.Domain}>
                  <td className="mono" style={{ fontWeight: 600 }}>{item.Domain}</td>
                  <td>{item.Name || item.Domain}</td>
                  <td className="mono">{item.SMTPHost}</td>
                  <td className="mono">{item.SMTPPort}</td>
                  <td>{item.TLSEnabled ? "TLS" : "Plaintext"}</td>
                  <td>{item.Active ? "Active" : "Inactive"}</td>
                  <td className="mono">{asArray(item.AllowedIPs).length > 0 ? asArray(item.AllowedIPs).join(", ") : "—"}</td>
                  <td>
                    <div className="flex gap-8">
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => openEdit(item)}>
                        Edit
                      </button>
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => void removePeer(item.Domain)}>
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showAdd ? (
        <Modal title={editing ? "Edit MM4 Peer" : "Add MM4 Peer"} onClose={() => { setShowAdd(false); setEditing(null); }} size="lg">
          <form onSubmit={submit}>
            <div className="modal-body surface-stack">
              <div className="section-shell">
                <h2>Peer Identity</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Name</span>
                    <input className="input" value={form.Name} onChange={(event) => setForm({ ...form, Name: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Domain</span>
                    <input className="input" value={form.Domain} disabled={Boolean(editing)} onChange={(event) => setForm({ ...form, Domain: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>SMTP Host</span>
                    <input className="input" value={form.SMTPHost} onChange={(event) => setForm({ ...form, SMTPHost: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Port</span>
                    <input className="input" value={form.SMTPPort} onChange={(event) => setForm({ ...form, SMTPPort: event.target.value })} />
                  </label>
                </div>
              </div>
              <div className="section-shell">
                <h2>Ingress Controls</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Allowed IPs</span>
                    <input className="input" value={form.AllowedIPs} onChange={(event) => setForm({ ...form, AllowedIPs: event.target.value })} />
                  </label>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.TLSEnabled} onChange={(event) => setForm({ ...form, TLSEnabled: event.target.checked })} />
                    <span>TLS enabled</span>
                  </label>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.Active} onChange={(event) => setForm({ ...form, Active: event.target.checked })} />
                    <span>Active</span>
                  </label>
                </div>
              </div>
            </div>
            <div className="modal-footer">
              <button className="btn btn-ghost" type="button" onClick={() => setShowAdd(false)}>
                Cancel
              </button>
              <button className="btn btn-primary" type="submit">
                {editing ? "Save Changes" : "Add Peer"}
              </button>
            </div>
          </form>
        </Modal>
      ) : null}
    </div>
  );
}

function SMPPUpstreamsTab() {
  const status = useAPI<{ upstreams: SMPPStatus[] }>("/api/v1/smpp/status", { upstreams: [] }, 15000);
  const upstreams = useAPI<{ upstreams: SMPPUpstream[] }>("/api/v1/smpp/upstreams", { upstreams: [] }, 15000);
  const toast = useToast();
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<SMPPUpstream | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState({
    Name: "",
    Host: "",
    Port: "2775",
    SystemID: "",
    Password: "",
    SystemType: "",
    BindMode: "transceiver",
    EnquireLink: "30",
    ReconnectWait: "5",
    Active: true,
  });

  const statusList = asArray(status.data.upstreams);
  const upstreamList = asArray(upstreams.data.upstreams);

  function resetForm() {
    setForm({
      Name: "",
      Host: "",
      Port: "2775",
      SystemID: "",
      Password: "",
      SystemType: "",
      BindMode: "transceiver",
      EnquireLink: "30",
      ReconnectWait: "5",
      Active: true,
    });
  }

  function openAdd() {
    setEditing(null);
    setError("");
    resetForm();
    setShowAdd(true);
  }

  function openEdit(item: SMPPUpstream) {
    setEditing(item);
    setError("");
    setForm({
      Name: item.Name,
      Host: item.Host,
      Port: String(item.Port),
      SystemID: item.SystemID,
      Password: item.Password,
      SystemType: item.SystemType,
      BindMode: item.BindMode,
      EnquireLink: String(item.EnquireLink),
      ReconnectWait: String(item.ReconnectWait),
      Active: item.Active,
    });
    setShowAdd(true);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      setError("");
      const body = {
        Name: form.Name.trim(),
        Host: form.Host.trim(),
        Port: Number(form.Port) || 2775,
        SystemID: form.SystemID.trim(),
        Password: form.Password,
        SystemType: form.SystemType.trim(),
        BindMode: form.BindMode,
        EnquireLink: Number(form.EnquireLink) || 30,
        ReconnectWait: Number(form.ReconnectWait) || 5,
        Active: form.Active,
      };
      if (editing) {
        await sendJSON(`/api/v1/smpp/upstreams/${encodeURIComponent(editing.Name)}`, "PUT", body);
        toast.success("SMPP peer updated", editing.Name);
      } else {
        await sendJSON("/api/v1/smpp/upstreams", "POST", body);
        toast.success("SMPP peer created", form.Name.trim());
      }
      resetForm();
      await Promise.all([status.reload(), upstreams.reload()]);
      setShowAdd(false);
      setEditing(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : "upstream save failed";
      setError(message);
      toast.error("Save failed", message);
    }
  }

  async function removePeer(name: string) {
    if (!window.confirm(`Delete SMPP peer ${name}?`)) {
      return;
    }
    try {
      setError("");
      await sendRequest(`/api/v1/smpp/upstreams/${encodeURIComponent(name)}`, "DELETE");
      await Promise.all([status.reload(), upstreams.reload()]);
      toast.success("SMPP peer deleted", name);
    } catch (err) {
      const message = err instanceof Error ? err.message : "peer delete failed";
      setError(message);
      toast.error("Delete failed", message);
    }
  }

  const statusByName = new Map(statusList.map((item) => [item.name, item]));
  const boundCount = statusList.filter((item) => item.state === 2).length;
  const activeCount = upstreamList.filter((item) => item.Active).length;

  return (
    <div className="stack">
      <div className="grid">
        <div className="card summary-card">
          <span className="pill">Configured Upstreams</span>
          <strong>{upstreamList.length}</strong>
          <div className="summary-card-copy">SMPP systems defined for WAP Push delivery.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Active</span>
          <strong>{activeCount}</strong>
          <div className="summary-card-copy">Upstreams enabled for connection management.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Bound</span>
          <strong>{boundCount}</strong>
          <div className="summary-card-copy">Live sessions currently bound to upstream SMSCs.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Delivery Path</span>
          <strong>WAP Push</strong>
          <div className="summary-card-copy">SMPP transport used for handset MMS notifications.</div>
        </div>
      </div>

      <div className="flex justify-between mb-12">
        <span className="text-muted text-sm">SMPP transport used by the MMSC notification path</span>
        <div className="flex gap-8">
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => void Promise.all([status.reload(), upstreams.reload()])}>
            Refresh
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={openAdd}>
            Add Peer
          </button>
        </div>
      </div>

      {(error || status.error || upstreams.error) && <div className="notice error">{error || status.error || upstreams.error}</div>}

      {upstreamList.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-title">No SMPP peers configured</div>
          <div className="text-muted text-sm">Add an SMPP peer to deliver WAP Push notifications.</div>
          <button className="btn btn-primary mt-12" type="button" onClick={openAdd}>
            Add First Peer
          </button>
        </div>
      ) : (
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Host</th>
                <th>Port</th>
                <th>System ID</th>
                <th>Bind Mode</th>
                <th>State</th>
                <th>Enquire</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {upstreamList.map((item) => {
                const live = statusByName.get(item.Name);
                const state = live ? smppStateLabel(live.state) : "Disconnected";
                return (
                  <tr key={item.Name}>
                    <td className="mono" style={{ fontWeight: 600 }}>{item.Name}</td>
                    <td className="mono">{item.Host}</td>
                    <td className="mono">{item.Port}</td>
                    <td className="mono">{item.SystemID}</td>
                    <td>{item.BindMode}</td>
                    <td>{state}</td>
                    <td className="mono">{item.EnquireLink}s</td>
                    <td>
                      <div className="flex gap-8">
                        <button className="btn btn-ghost btn-sm" type="button" onClick={() => openEdit(item)}>
                          Edit
                        </button>
                        <button className="btn btn-ghost btn-sm" type="button" onClick={() => void removePeer(item.Name)}>
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {showAdd ? (
        <Modal title={editing ? "Edit SMPP Peer" : "Add SMPP Peer"} onClose={() => { setShowAdd(false); setEditing(null); }} size="lg">
          <form onSubmit={submit}>
            <div className="modal-body surface-stack">
              <div className="section-shell">
                <h2>Endpoint</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Name</span>
                    <input className="input" value={form.Name} disabled={Boolean(editing)} onChange={(event) => setForm({ ...form, Name: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Host</span>
                    <input className="input" value={form.Host} onChange={(event) => setForm({ ...form, Host: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Port</span>
                    <input className="input" value={form.Port} onChange={(event) => setForm({ ...form, Port: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>System ID</span>
                    <input className="input" value={form.SystemID} onChange={(event) => setForm({ ...form, SystemID: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Password</span>
                    <input className="input" type="password" value={form.Password} onChange={(event) => setForm({ ...form, Password: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>System Type</span>
                    <input className="input" value={form.SystemType} onChange={(event) => setForm({ ...form, SystemType: event.target.value })} />
                  </label>
                </div>
              </div>
              <div className="section-shell">
                <h2>Session Behaviour</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Bind Mode</span>
                    <select className="input" value={form.BindMode} onChange={(event) => setForm({ ...form, BindMode: event.target.value })}>
                      <option value="transceiver">transceiver</option>
                      <option value="transmitter">transmitter</option>
                      <option value="receiver">receiver</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>Enquire Link</span>
                    <input className="input" value={form.EnquireLink} onChange={(event) => setForm({ ...form, EnquireLink: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Reconnect Wait</span>
                    <input className="input" value={form.ReconnectWait} onChange={(event) => setForm({ ...form, ReconnectWait: event.target.value })} />
                  </label>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.Active} onChange={(event) => setForm({ ...form, Active: event.target.checked })} />
                    <span>Active</span>
                  </label>
                </div>
              </div>
            </div>
            <div className="modal-footer">
              <button className="btn btn-ghost" type="button" onClick={() => { setShowAdd(false); setEditing(null); }}>
                Cancel
              </button>
              <button className="btn btn-primary" type="submit">
                {editing ? "Save Changes" : "Add Peer"}
              </button>
            </div>
          </form>
        </Modal>
      ) : null}
    </div>
  );
}
