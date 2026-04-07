import { FormEvent, useEffect, useState } from "react";

import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { MM3Relay, sendJSON, useAPI } from "../lib/api";

const emptyRelay: MM3Relay = {
  Enabled: false,
  SMTPHost: "",
  SMTPPort: 25,
  SMTPAuth: false,
  SMTPUser: "",
  SMTPPass: "",
  TLSEnabled: false,
  DefaultSenderDomain: "",
  DefaultFromAddress: "",
};

export function MM3() {
  const relay = useAPI<MM3Relay>("/api/v1/mm3/relay", emptyRelay, 15000);
  const toast = useToast();
  const [showEditor, setShowEditor] = useState(false);
  const [form, setForm] = useState({
    Enabled: false,
    SMTPHost: "",
    SMTPPort: "25",
    SMTPAuth: false,
    SMTPUser: "",
    SMTPPass: "",
    TLSEnabled: false,
    DefaultSenderDomain: "",
    DefaultFromAddress: "",
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [savedAt, setSavedAt] = useState("");

  useEffect(() => {
    setForm({
      Enabled: relay.data.Enabled,
      SMTPHost: relay.data.SMTPHost || "",
      SMTPPort: String(relay.data.SMTPPort || 25),
      SMTPAuth: relay.data.SMTPAuth,
      SMTPUser: relay.data.SMTPUser || "",
      SMTPPass: relay.data.SMTPPass || "",
      TLSEnabled: relay.data.TLSEnabled,
      DefaultSenderDomain: relay.data.DefaultSenderDomain || "",
      DefaultFromAddress: relay.data.DefaultFromAddress || "",
    });
  }, [relay.data]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      setSaving(true);
      setError("");
      setSavedAt("");
      await sendJSON<MM3Relay>("/api/v1/mm3/relay", "PUT", {
        Enabled: form.Enabled,
        SMTPHost: form.SMTPHost.trim(),
        SMTPPort: Number(form.SMTPPort) || 25,
        SMTPAuth: form.SMTPAuth,
        SMTPUser: form.SMTPUser.trim(),
        SMTPPass: form.SMTPPass,
        TLSEnabled: form.TLSEnabled,
        DefaultSenderDomain: form.DefaultSenderDomain.trim(),
        DefaultFromAddress: form.DefaultFromAddress.trim(),
      });
      await relay.reload();
      setSavedAt(new Date().toLocaleTimeString());
      setShowEditor(false);
      toast.success("MM3 relay saved", form.SMTPHost.trim() || "Runtime config updated");
    } catch (err) {
      const message = err instanceof Error ? err.message : "mm3 relay save failed";
      setError(message);
      toast.error("Save failed", message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="stack">
      {(error || relay.error) && <div className="notice error">{error || relay.error}</div>}

      <div className="grid">
        <div className="card summary-card">
          <span className="pill">Relay State</span>
          <strong>{relay.data.Enabled ? "Enabled" : "Disabled"}</strong>
          <div className="summary-card-copy">Outbound MM3 email relay availability in active runtime.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Transport</span>
          <strong>{relay.data.SMTPHost ? `${relay.data.SMTPHost}:${relay.data.SMTPPort}` : "Not Set"}</strong>
          <div className="summary-card-copy">SMTP endpoint used for MMS-to-email relay.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Session Mode</span>
          <strong>{relay.data.SMTPAuth ? "AUTH" : "Open"} / {relay.data.TLSEnabled ? "TLS" : "Plain"}</strong>
          <div className="summary-card-copy">Current authentication and encryption posture.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Sender Identity</span>
          <strong>{relay.data.DefaultSenderDomain || relay.data.DefaultFromAddress || "Not Set"}</strong>
          <div className="summary-card-copy">Default sender normalization for email-originated traffic.</div>
        </div>
      </div>

      <div className="section-shell">
        <div className="flex justify-between mb-12">
          <div>
            <h2>MM3 Operations</h2>
            <div className="text-muted text-sm">Outbound relay posture and sender-normalization defaults for MMS-to-email flows.</div>
          </div>
          <div className="flex gap-8">
            {savedAt ? <span className="text-muted text-sm">Saved at {savedAt}</span> : null}
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => void relay.reload()}>
              Refresh
            </button>
            <button className="btn btn-primary btn-sm" type="button" onClick={() => setShowEditor(true)}>
              Edit Relay
            </button>
          </div>
        </div>

        <div className="surface-stack">
          <div className="surface-note">
            MM3 stays narrow on purpose: native SMTP ingress and relay for Mbuni-level email gatewaying, without depending on an external MTA.
          </div>
          <div className="split">
            <div className="section-shell">
              <h2>Transport</h2>
              <div className="table-wrap">
                <table>
                  <tbody>
                    <tr>
                      <td><strong>State</strong></td>
                      <td>{relay.data.Enabled ? "Enabled" : "Disabled"}</td>
                    </tr>
                    <tr>
                      <td><strong>SMTP Endpoint</strong></td>
                      <td className="mono">{relay.data.SMTPHost ? `${relay.data.SMTPHost}:${relay.data.SMTPPort}` : "Not configured"}</td>
                    </tr>
                    <tr>
                      <td><strong>Session Mode</strong></td>
                      <td>{relay.data.SMTPAuth ? "SMTP AUTH" : "Open relay auth"} / {relay.data.TLSEnabled ? "STARTTLS" : "Plain SMTP"}</td>
                    </tr>
                    <tr>
                      <td><strong>User</strong></td>
                      <td className="mono">{relay.data.SMTPUser || "—"}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div className="section-shell">
              <h2>Sender Normalization</h2>
              <div className="page-subtitle">Defaults applied when inbound email does not provide a carrier-usable sender identity.</div>
              <div className="table-wrap mt-16">
                <table>
                  <tbody>
                    <tr>
                      <td><strong>Default Domain</strong></td>
                      <td className="mono">{relay.data.DefaultSenderDomain || "—"}</td>
                    </tr>
                    <tr>
                      <td><strong>Default From</strong></td>
                      <td className="mono">{relay.data.DefaultFromAddress || "—"}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <div className="mm3-hint">
                {relay.data.Enabled ? "Relay enabled" : "Relay disabled"}
                {relay.data.SMTPHost ? ` • ${relay.data.SMTPHost}:${relay.data.SMTPPort}` : ""}
              </div>
            </div>
          </div>
        </div>
      </div>

      {showEditor ? (
        <Modal title="Edit MM3 Relay" onClose={() => setShowEditor(false)} size="lg">
          <form onSubmit={submit}>
            <div className="modal-body surface-stack">
              <div className="surface-note">
                Changes are runtime-backed and apply without restart. Bootstrap YAML still remains restart-bound.
              </div>
              <div className="section-shell">
                <h2>Transport</h2>
                <div className="surface-grid">
                  <label className="checkbox">
                    <input type="checkbox" checked={form.Enabled} onChange={(event) => setForm({ ...form, Enabled: event.target.checked })} />
                    <span>Enable outbound MM3 relay</span>
                  </label>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.TLSEnabled} onChange={(event) => setForm({ ...form, TLSEnabled: event.target.checked })} />
                    <span>Use STARTTLS</span>
                  </label>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.SMTPAuth} onChange={(event) => setForm({ ...form, SMTPAuth: event.target.checked })} />
                    <span>Use SMTP AUTH</span>
                  </label>
                  <div />
                  <label className="field">
                    <span>SMTP Host</span>
                    <input className="input" value={form.SMTPHost} onChange={(event) => setForm({ ...form, SMTPHost: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Port</span>
                    <input className="input" value={form.SMTPPort} onChange={(event) => setForm({ ...form, SMTPPort: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>SMTP User</span>
                    <input className="input" value={form.SMTPUser} onChange={(event) => setForm({ ...form, SMTPUser: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>SMTP Password</span>
                    <input className="input" type="password" value={form.SMTPPass} onChange={(event) => setForm({ ...form, SMTPPass: event.target.value })} />
                  </label>
                </div>
              </div>
              <div className="section-shell">
                <h2>Sender Normalization</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Default Sender Domain</span>
                    <input className="input" value={form.DefaultSenderDomain} onChange={(event) => setForm({ ...form, DefaultSenderDomain: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Default From Address</span>
                    <input className="input" value={form.DefaultFromAddress} onChange={(event) => setForm({ ...form, DefaultFromAddress: event.target.value })} />
                  </label>
                </div>
              </div>
              {error && <div className="notice error">{error}</div>}
            </div>
            <div className="modal-footer">
              <button className="btn btn-ghost" type="button" onClick={() => { void relay.reload(); setShowEditor(false); }}>
                Cancel
              </button>
              <button className="btn btn-primary" type="submit" disabled={saving}>
                {saving ? "Saving..." : "Save MM3 Relay"}
              </button>
            </div>
          </form>
        </Modal>
      ) : null}
    </div>
  );
}
