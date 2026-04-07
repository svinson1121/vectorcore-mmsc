import { FormEvent, useState } from "react";

import { useToast } from "../components/Toast";
import { Modal } from "../components/Modal";
import { asArray, csv, formatBytes, sendJSON, useAPI, VASP } from "../lib/api";

type VASPFormState = {
  VASPID: string;
  VASID: string;
  Protocol: string;
  Version: string;
  SharedSecret: string;
  AllowedIPs: string;
  DeliverURL: string;
  ReportURL: string;
  MaxMsgSize: string;
  Active: boolean;
};

const defaultForm: VASPFormState = {
  VASPID: "",
  VASID: "",
  Protocol: "soap",
  Version: "",
  SharedSecret: "",
  AllowedIPs: "",
  DeliverURL: "",
  ReportURL: "",
  MaxMsgSize: "1048576",
  Active: true,
};

export function VASPs() {
  const vasps = useAPI<{ vasps: VASP[] }>("/api/v1/vasps", { vasps: [] }, 15000);
  const toast = useToast();
  const vaspList = asArray(vasps.data.vasps);
  const [showEditor, setShowEditor] = useState(false);
  const [editing, setEditing] = useState<VASP | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState<VASPFormState>(defaultForm);

  const activeCount = vaspList.filter((item) => item.Active).length;
  const soapCount = vaspList.filter((item) => effectiveProtocol(item) === "soap").length;
  const eaifCount = vaspList.filter((item) => effectiveProtocol(item) === "eaif").length;
  const callbackCount = vaspList.filter((item) => item.DeliverURL || item.ReportURL).length;
  const allowlistCount = vaspList.filter((item) => asArray(item.AllowedIPs).length > 0).length;

  function resetForm() {
    setForm(defaultForm);
  }

  function openAdd() {
    setEditing(null);
    setError("");
    resetForm();
    setShowEditor(true);
  }

  function openEdit(item: VASP) {
    setEditing(item);
    setError("");
    setForm({
      VASPID: item.VASPID,
      VASID: item.VASID,
      Protocol: effectiveProtocol(item),
      Version: item.Version || "",
      SharedSecret: item.SharedSecret || "",
      AllowedIPs: asArray(item.AllowedIPs).join(", "),
      DeliverURL: item.DeliverURL || "",
      ReportURL: item.ReportURL || "",
      MaxMsgSize: String(item.MaxMsgSize || 1048576),
      Active: item.Active,
    });
    setShowEditor(true);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      setError("");
      const body = {
        VASPID: form.VASPID.trim(),
        VASID: form.VASID.trim(),
        Protocol: form.Protocol,
        Version: form.Version.trim(),
        SharedSecret: form.SharedSecret,
        AllowedIPs: csv(form.AllowedIPs),
        DeliverURL: form.DeliverURL.trim(),
        ReportURL: form.ReportURL.trim(),
        MaxMsgSize: Number(form.MaxMsgSize) || 0,
        Active: form.Active,
      };
      if (editing) {
        await sendJSON(`/api/v1/vasps/${encodeURIComponent(editing.VASPID)}`, "PUT", body);
        toast.success("VASP updated", editing.VASPID);
      } else {
        await sendJSON("/api/v1/vasps", "POST", body);
        toast.success("VASP created", form.VASPID.trim());
      }
      resetForm();
      await vasps.reload();
      setShowEditor(false);
      setEditing(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : "vasp save failed";
      setError(message);
      toast.error("Save failed", message);
    }
  }

  async function removeVASP(vaspID: string) {
    if (!window.confirm(`Delete VASP ${vaspID}?`)) {
      return;
    }
    try {
      setError("");
      await sendJSON(`/api/v1/vasps/${encodeURIComponent(vaspID)}`, "DELETE", null);
      await vasps.reload();
      toast.success("VASP deleted", vaspID);
    } catch (err) {
      const message = err instanceof Error ? err.message : "vasp delete failed";
      setError(message);
      toast.error("Delete failed", message);
    }
  }

  return (
    <div className="stack">
      <div className="grid">
        <div className="card summary-card">
          <span className="pill">Configured VASPs</span>
          <strong>{vaspList.length}</strong>
          <div className="summary-card-copy">MM7 application endpoints currently defined in operator config.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Active</span>
          <strong>{activeCount}</strong>
          <div className="summary-card-copy">Endpoints eligible for inbound submit traffic and outbound callbacks.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Protocol Mix</span>
          <strong>
            {soapCount} SOAP / {eaifCount} EAIF
          </strong>
          <div className="summary-card-copy">Mbuni-aligned MM7 transport split across configured VASPs.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Callback Ready</span>
          <strong>{callbackCount}</strong>
          <div className="summary-card-copy">VASPs with deliver or report callback destinations configured.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">IP Controls</span>
          <strong>{allowlistCount}</strong>
          <div className="summary-card-copy">VASPs with inbound source allowlists applied.</div>
        </div>
      </div>

      <div className="flex justify-between mb-12">
        <span className="text-muted text-sm">MM7 SOAP and EAIF endpoint management, callback routing, and ingress controls</span>
        <div className="flex gap-8">
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => void vasps.reload()}>
            Refresh
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={openAdd}>
            Add VASP
          </button>
        </div>
      </div>

      {(error || vasps.error) && <div className="notice error">{error || vasps.error}</div>}

      {vaspList.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-title">No MM7 VASPs configured</div>
          <div className="text-muted text-sm">Add a VASP to accept submit traffic and return delivery or read-report callbacks.</div>
          <button className="btn btn-primary mt-12" type="button" onClick={openAdd}>
            Add First VASP
          </button>
        </div>
      ) : (
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>VASP ID</th>
                <th>Protocol</th>
                <th>Callback Path</th>
                <th>Security</th>
                <th>Message Limit</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {vaspList.map((item) => {
                const protocol = effectiveProtocol(item);
                const callbacks = callbackSummary(item);
                const security = securitySummary(item);
                return (
                  <tr key={item.VASPID}>
                    <td>
                      <div style={{ display: "grid", gap: 4 }}>
                        <span className="mono" style={{ fontWeight: 600 }}>{item.VASPID}</span>
                        <span className="text-muted text-sm">{item.VASID || "No VAS ID"}</span>
                      </div>
                    </td>
                    <td>
                      <div style={{ display: "grid", gap: 4 }}>
                        <span className={`badge ${protocol === "eaif" ? "badge-warning" : "badge-info"}`}>{protocol.toUpperCase()}</span>
                        <span className="mono text-sm">{effectiveVersion(item)}</span>
                      </div>
                    </td>
                    <td>
                      <div style={{ display: "grid", gap: 4 }}>
                        <span className="text-sm">{callbacks.primary}</span>
                        <span className="mono text-sm">{callbacks.secondary}</span>
                      </div>
                    </td>
                    <td>
                      <div style={{ display: "grid", gap: 4 }}>
                        <span className="text-sm">{security.primary}</span>
                        <span className="mono text-sm">{security.secondary}</span>
                      </div>
                    </td>
                    <td className="mono">{formatBytes(item.MaxMsgSize || 0)}</td>
                    <td>
                      <span className={`badge ${item.Active ? "badge-success" : "badge-muted"}`}>{item.Active ? "ACTIVE" : "INACTIVE"}</span>
                    </td>
                    <td>
                      <div className="flex gap-8">
                        <button className="btn btn-ghost btn-sm" type="button" onClick={() => openEdit(item)}>
                          Edit
                        </button>
                        <button className="btn btn-ghost btn-sm" type="button" onClick={() => void removeVASP(item.VASPID)}>
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

      {showEditor ? (
        <Modal title={editing ? "Edit MM7 VASP" : "Add MM7 VASP"} onClose={() => { setShowEditor(false); setEditing(null); }} size="lg">
          <form onSubmit={submit}>
            <div className="modal-body surface-stack">
              <div className="surface-note">
                {form.Protocol === "eaif"
                  ? "EAIF mode uses Nokia-style header exchange over HTTP with binary MMS bodies."
                  : "SOAP mode uses MM7 SOAP request and callback envelopes with the configured version and namespace mapping."}
              </div>
              <div className="section-shell">
                <h2>Identity And Protocol</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>VASP ID</span>
                    <input className="input" value={form.VASPID} disabled={Boolean(editing)} onChange={(event) => setForm({ ...form, VASPID: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>VAS ID</span>
                    <input className="input" value={form.VASID} onChange={(event) => setForm({ ...form, VASID: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Protocol</span>
                    <select className="input" value={form.Protocol} onChange={(event) => setForm({ ...form, Protocol: event.target.value })}>
                      <option value="soap">SOAP</option>
                      <option value="eaif">EAIF</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>Protocol Version</span>
                    <input className="input" value={form.Version} placeholder={form.Protocol === "eaif" ? "3.0" : "5.3.0"} onChange={(event) => setForm({ ...form, Version: event.target.value })} />
                  </label>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.Active} onChange={(event) => setForm({ ...form, Active: event.target.checked })} />
                    <span>Active</span>
                  </label>
                </div>
              </div>
              <div className="section-shell">
                <h2>Security And Callbacks</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Shared Secret</span>
                    <input
                      className="input"
                      type="password"
                      value={form.SharedSecret}
                      onChange={(event) => setForm({ ...form, SharedSecret: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    <span>Allowed IPs</span>
                    <input className="input" placeholder="198.51.100.10, 198.51.100.11" value={form.AllowedIPs} onChange={(event) => setForm({ ...form, AllowedIPs: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Deliver URL</span>
                    <input className="input" placeholder="https://vasp.example.net/deliver" value={form.DeliverURL} onChange={(event) => setForm({ ...form, DeliverURL: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Report URL</span>
                    <input className="input" placeholder="https://vasp.example.net/report" value={form.ReportURL} onChange={(event) => setForm({ ...form, ReportURL: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Max Message Size</span>
                    <input className="input" value={form.MaxMsgSize} onChange={(event) => setForm({ ...form, MaxMsgSize: event.target.value })} />
                  </label>
                </div>
              </div>
              {error && <div className="notice error">{error}</div>}
            </div>
            <div className="modal-footer">
              <button className="btn btn-ghost" type="button" onClick={() => { setShowEditor(false); setEditing(null); }}>
                Cancel
              </button>
              <button className="btn btn-primary" type="submit">
                {editing ? "Save Changes" : "Add VASP"}
              </button>
            </div>
          </form>
        </Modal>
      ) : null}
    </div>
  );
}

function effectiveProtocol(item: Pick<VASP, "Protocol">): string {
  return item.Protocol || "soap";
}

function effectiveVersion(item: Pick<VASP, "Protocol" | "Version">): string {
  if (item.Version) {
    return item.Version;
  }
  return effectiveProtocol(item) === "eaif" ? "3.0" : "5.3.0";
}

function callbackSummary(item: VASP): { primary: string; secondary: string } {
  if (item.DeliverURL && item.ReportURL) {
    return {
      primary: "Deliver + report callbacks",
      secondary: `${shortURL(item.DeliverURL)} | ${shortURL(item.ReportURL)}`,
    };
  }
  if (item.DeliverURL) {
    return {
      primary: "Deliver callback only",
      secondary: shortURL(item.DeliverURL),
    };
  }
  if (item.ReportURL) {
    return {
      primary: "Report callback only",
      secondary: shortURL(item.ReportURL),
    };
  }
  return {
    primary: "No callback URLs",
    secondary: "Outbound callbacks disabled",
  };
}

function securitySummary(item: VASP): { primary: string; secondary: string } {
  const allowlist = asArray(item.AllowedIPs);
  const auth = item.SharedSecret ? "Shared secret set" : "No shared secret";
  if (allowlist.length === 0) {
    return {
      primary: auth,
      secondary: "No IP allowlist",
    };
  }
  return {
    primary: auth,
    secondary: `${allowlist.length} allowed IP${allowlist.length === 1 ? "" : "s"}`,
  };
}

function shortURL(value: string): string {
  try {
    const parsed = new URL(value);
    return `${parsed.host}${parsed.pathname}`;
  } catch {
    return value;
  }
}
