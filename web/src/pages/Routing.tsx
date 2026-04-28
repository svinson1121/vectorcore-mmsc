import { FormEvent, useState } from "react";

import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { asArray, MM4Route, Peer, sendJSON, sendRequest, useAPI } from "../lib/api";

export function Routing() {
  const routes = useAPI<{ routes: MM4Route[] }>("/api/v1/routes", { routes: [] }, 15000);
  const peers = useAPI<{ peers: Peer[] }>("/api/v1/peers", { peers: [] }, 15000);
  const toast = useToast();
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<MM4Route | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState({
    Name: "",
    MatchType: "msisdn_prefix",
    MatchValue: "",
    Egress: "reject",
    Priority: "100",
    Active: true,
  });

  const routeList = asArray(routes.data.routes);
  const peerList = asArray(peers.data.peers);
  const peerName = new Map(peerList.map((item) => [item.Domain, item.Name || item.Domain]));
  const activeRoutes = routeList.filter((item) => item.Active).length;
  const rejectRoutes = routeList.filter((item) => item.EgressType === "reject").length;
  const mm4Routes = routeList.filter((item) => item.EgressType === "mm4").length;

  function resetForm() {
    setForm({
      Name: "",
      MatchType: "msisdn_prefix",
      MatchValue: "",
      Egress: "reject",
      Priority: "100",
      Active: true,
    });
  }

  function openAdd() {
    setEditing(null);
    setError("");
    resetForm();
    setShowAdd(true);
  }

  function openEdit(item: MM4Route) {
    setEditing(item);
    setError("");
    setForm({
      Name: item.Name,
      MatchType: item.MatchType,
      MatchValue: item.MatchValue,
      Egress: egressValue(item),
      Priority: String(item.Priority),
      Active: item.Active,
    });
    setShowAdd(true);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      setError("");
      const parsed = parseEgress(form.Egress);
      const body = {
        ID: editing?.ID || 0,
        Name: form.Name.trim(),
        MatchType: form.MatchType,
        MatchValue: form.MatchValue.trim(),
        EgressType: parsed.type,
        EgressTarget: parsed.target,
        EgressPeerDomain: parsed.type === "mm4" ? parsed.target : "",
        Priority: Number(form.Priority) || 100,
        Active: form.Active,
      };
      if (editing) {
        await sendJSON(`/api/v1/routes/${editing.ID}`, "PUT", body);
        toast.success("Route updated", form.Name.trim());
      } else {
        await sendJSON("/api/v1/routes", "POST", body);
        toast.success("Route created", form.Name.trim());
      }
      resetForm();
      await routes.reload();
      setShowAdd(false);
      setEditing(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : "route save failed";
      setError(message);
      toast.error("Save failed", message);
    }
  }

  async function removeRoute(route: MM4Route) {
    if (!window.confirm(`Delete route ${route.Name}?`)) {
      return;
    }
    try {
      setError("");
      await sendRequest(`/api/v1/routes/${route.ID}`, "DELETE");
      await routes.reload();
      toast.success("Route deleted", route.Name);
    } catch (err) {
      const message = err instanceof Error ? err.message : "route delete failed";
      setError(message);
      toast.error("Delete failed", message);
    }
  }

  return (
    <div className="stack">
      <div className="grid">
        <div className="card summary-card">
          <span className="pill">Routing Rules</span>
          <strong>{routeList.length}</strong>
          <div className="summary-card-copy">Explicit recipient routing policy entries.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Active</span>
          <strong>{activeRoutes}</strong>
          <div className="summary-card-copy">Rules eligible to handle recipient resolution.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">MM4</span>
          <strong>{mm4Routes}</strong>
          <div className="summary-card-copy">Rules egressing to inter-MMSC peers.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Reject</span>
          <strong>{rejectRoutes}</strong>
          <div className="summary-card-copy">Rules explicitly rejecting matched recipients.</div>
        </div>
      </div>

      <div className="flex justify-between mb-12">
        <span className="text-muted text-sm">Recipient matching and egress selection</span>
        <div className="flex gap-8">
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => void Promise.all([routes.reload(), peers.reload()])}>
            Refresh
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={openAdd}>
            Add Route
          </button>
        </div>
      </div>

      {(error || routes.error || peers.error) && <div className="notice error">{error || routes.error || peers.error}</div>}

      {routeList.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-title">No routing rules configured</div>
          <div className="text-muted text-sm">Rules override default handling. Unmatched MSISDN recipients fall back to local delivery.</div>
          <button className="btn btn-primary mt-12" type="button" onClick={openAdd}>
            Add First Route
          </button>
        </div>
      ) : (
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Match Type</th>
                <th>Match Value</th>
                <th>Egress</th>
                <th>Priority</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {routeList.map((item) => (
                <tr key={item.ID}>
                  <td>{item.Name}</td>
                  <td>{routeTypeLabel(item.MatchType)}</td>
                  <td className="mono">{item.MatchValue}</td>
                  <td>{egressLabel(item, peerName)}</td>
                  <td className="mono">{item.Priority}</td>
                  <td>{item.Active ? "Active" : "Inactive"}</td>
                  <td>
                    <div className="flex gap-8">
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => openEdit(item)}>
                        Edit
                      </button>
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => void removeRoute(item)}>
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
        <Modal title={editing ? "Edit Route" : "Add Route"} onClose={() => { setShowAdd(false); setEditing(null); }} size="lg">
          <form onSubmit={submit}>
            <div className="modal-body surface-stack">
              <div className="section-shell">
                <h2>Route Match</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Name</span>
                    <input className="input" value={form.Name} onChange={(event) => setForm({ ...form, Name: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Match Type</span>
                    <select className="input" value={form.MatchType} onChange={(event) => setForm({ ...form, MatchType: event.target.value })}>
                      <option value="msisdn_prefix">MSISDN prefix</option>
                      <option value="msisdn_exact">MSISDN exact</option>
                      <option value="recipient_domain">Recipient domain</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>Match Value</span>
                    <input className="input" value={form.MatchValue} onChange={(event) => setForm({ ...form, MatchValue: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Egress</span>
                    <select className="input" value={form.Egress} onChange={(event) => setForm({ ...form, Egress: event.target.value })}>
                      <option value="local">Local delivery</option>
                      <option value="reject">Reject message</option>
                      <option value="mm3">MM3 relay</option>
                      {peerList.map((item) => (
                        <option key={item.Domain} value={`mm4:${item.Domain}`}>MM4: {item.Name || item.Domain}</option>
                      ))}
                    </select>
                  </label>
                  <label className="field">
                    <span>Priority</span>
                    <input className="input" value={form.Priority} onChange={(event) => setForm({ ...form, Priority: event.target.value })} />
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
                {editing ? "Save Changes" : "Add Route"}
              </button>
            </div>
          </form>
        </Modal>
      ) : null}
    </div>
  );
}

function routeTypeLabel(value: string): string {
  switch (value) {
    case "msisdn_exact":
      return "MSISDN exact";
    case "msisdn_prefix":
      return "MSISDN prefix";
    case "recipient_domain":
      return "Recipient domain";
    default:
      return value;
  }
}

function egressValue(route: MM4Route): string {
  if (route.EgressType === "mm4") {
    return `mm4:${route.EgressTarget || route.EgressPeerDomain}`;
  }
  return route.EgressType || "reject";
}

function parseEgress(value: string): { type: string; target: string } {
  if (value.startsWith("mm4:")) {
    return { type: "mm4", target: value.slice("mm4:".length) };
  }
  return { type: value, target: "" };
}

function egressLabel(route: MM4Route, peerName: Map<string, string>): string {
  switch (route.EgressType) {
    case "local":
      return "Local delivery";
    case "reject":
      return "Reject message";
    case "mm3":
      return "MM3 relay";
    case "mm4": {
      const target = route.EgressTarget || route.EgressPeerDomain;
      return `MM4: ${peerName.get(target) || target}`;
    }
    default:
      return route.EgressType || "Reject message";
  }
}
