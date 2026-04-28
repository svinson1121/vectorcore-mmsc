import { FormEvent, useState } from "react";

import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { AdaptationClass, asArray, csv, sendJSON, sendRequest, SystemConfig, useAPI } from "../lib/api";

type AdaptationFormState = {
  Name: string;
  MaxMsgSizeBytes: string;
  MaxImageWidth: string;
  MaxImageHeight: string;
  AllowedImageTypes: string;
  AllowedAudioTypes: string;
  AllowedVideoTypes: string;
};

const defaultForm: AdaptationFormState = {
  Name: "",
  MaxMsgSizeBytes: "307200",
  MaxImageWidth: "640",
  MaxImageHeight: "480",
  AllowedImageTypes: "image/jpeg,image/gif,image/png",
  AllowedAudioTypes: "audio/amr,audio/mpeg,audio/mp4",
  AllowedVideoTypes: "video/3gpp,video/mp4",
};

export function Adaptation() {
  const sysConfig = useAPI<SystemConfig>("/api/v1/system/config", { adapt_enabled: true, max_message_size_bytes: 0 });
  const classes = useAPI<{ classes: AdaptationClass[] }>("/api/v1/adaptation/classes", { classes: [] }, 15000);
  const toast = useToast();
  const classList = asArray(classes.data.classes);
  const [showEditor, setShowEditor] = useState(false);
  const [editing, setEditing] = useState<AdaptationClass | null>(null);
  const [form, setForm] = useState<AdaptationFormState>(defaultForm);
  const [error, setError] = useState("");
  const defaultClass = classList.find((item) => item.Name === "default");
  const nonDefaultCount = classList.filter((item) => item.Name !== "default").length;
  const imageRestrictedCount = classList.filter((item) => asArray(item.AllowedImageTypes).length > 0).length;
  const videoEnabledCount = classList.filter((item) => asArray(item.AllowedVideoTypes).length > 0).length;

  function resetForm() {
    setForm(defaultForm);
  }

  function openAdd() {
    setEditing(null);
    setError("");
    resetForm();
    setShowEditor(true);
  }

  function openEdit(item: AdaptationClass) {
    setEditing(item);
    setError("");
    setForm({
      Name: item.Name,
      MaxMsgSizeBytes: String(item.MaxMsgSizeBytes || 0),
      MaxImageWidth: String(item.MaxImageWidth || 0),
      MaxImageHeight: String(item.MaxImageHeight || 0),
      AllowedImageTypes: asArray(item.AllowedImageTypes).join(", "),
      AllowedAudioTypes: asArray(item.AllowedAudioTypes).join(", "),
      AllowedVideoTypes: asArray(item.AllowedVideoTypes).join(", "),
    });
    setShowEditor(true);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      setError("");
      const body = {
        Name: form.Name.trim(),
        MaxMsgSizeBytes: Number(form.MaxMsgSizeBytes) || 0,
        MaxImageWidth: Number(form.MaxImageWidth) || 0,
        MaxImageHeight: Number(form.MaxImageHeight) || 0,
        AllowedImageTypes: csv(form.AllowedImageTypes),
        AllowedAudioTypes: csv(form.AllowedAudioTypes),
        AllowedVideoTypes: csv(form.AllowedVideoTypes),
      };
      if (editing) {
        await sendJSON(`/api/v1/adaptation/classes/${encodeURIComponent(editing.Name)}`, "PUT", body);
        toast.success("Adaptation class updated", editing.Name);
      } else {
        await sendJSON("/api/v1/adaptation/classes", "POST", body);
        toast.success("Adaptation class created", form.Name.trim());
      }
      await classes.reload();
      resetForm();
      setShowEditor(false);
      setEditing(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : "adaptation policy save failed";
      setError(message);
      toast.error("Save failed", message);
    }
  }

  async function removeClass(name: string) {
    if (!window.confirm(`Delete adaptation class ${name}?`)) {
      return;
    }
    try {
      setError("");
      await sendRequest(`/api/v1/adaptation/classes/${encodeURIComponent(name)}`, "DELETE");
      await classes.reload();
      toast.success("Adaptation class deleted", name);
    } catch (err) {
      const message = err instanceof Error ? err.message : "adaptation class delete failed";
      setError(message);
      toast.error("Delete failed", message);
    }
  }

  return (
    <div className="stack">
      {!sysConfig.loading && !sysConfig.data.adapt_enabled && (
        <div className="notice warning">
          Adaptation is disabled in the global configuration. Class definitions are stored but not applied at runtime. To enable, set <code>adapt.enabled: true</code> in the YAML config and restart the service.
        </div>
      )}
      <div className="grid">
        <div className="card summary-card" title="Total adaptation classes available to runtime policy selection.">
          <span className="pill">Profiles</span>
          <strong>{classList.length}</strong>
          <div className="summary-card-copy">Runtime adaptation classes currently defined.</div>
        </div>
        <div className="card summary-card" title="The default class is the fallback when a subscriber or route does not select another profile.">
          <span className="pill">Default</span>
          <strong>{defaultClass ? "Present" : "Fallback"}</strong>
          <div className="summary-card-copy">Handset-facing default policy for unclassified traffic.</div>
        </div>
        <div className="card summary-card" title="Profiles beyond the default class can be assigned to device or partner policies later.">
          <span className="pill">Overrides</span>
          <strong>{nonDefaultCount}</strong>
          <div className="summary-card-copy">Named profiles beyond the baseline policy.</div>
        </div>
        <div className="card summary-card" title="Classes with image constraints or allowed image types set.">
          <span className="pill">Image Rules</span>
          <strong>{imageRestrictedCount}</strong>
          <div className="summary-card-copy">Profiles with image-policy metadata available.</div>
        </div>
        <div className="card summary-card" title="Classes permitting at least one video type.">
          <span className="pill">Video Ready</span>
          <strong>{videoEnabledCount}</strong>
          <div className="summary-card-copy">Profiles that currently allow video adaptation.</div>
        </div>
      </div>

      <div className="flex justify-between mb-12">
        <span className="text-muted text-sm">Constraint profiles for handset delivery, VASP policy, and future route-based adaptation decisions</span>
        <div className="flex gap-8">
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => void classes.reload()}>
            Refresh
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={openAdd}>
            Add Class
          </button>
        </div>
      </div>

      {(error || classes.error) && <div className="notice error">{error || classes.error}</div>}

      {classList.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-title">No adaptation classes defined</div>
          <div className="text-muted text-sm">Add a policy profile to control media size limits and allowed content types.</div>
          <button className="btn btn-primary mt-12" type="button" onClick={openAdd}>
            Add First Class
          </button>
        </div>
      ) : (
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>Class</th>
                <th>Message Limit</th>
                <th>Image Envelope</th>
                <th>Allowed Media</th>
                <th>Notes</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {classList.map((item) => (
                <tr key={item.Name}>
                  <td>
                    <div style={{ display: "grid", gap: 4 }}>
                      <span className="mono" style={{ fontWeight: 600 }}>{item.Name}</span>
                      <span className="text-muted text-sm">{item.Name === "default" ? "Fallback profile" : "Named override profile"}</span>
                    </div>
                  </td>
                  <td className="mono">{formatConstraintBytes(item.MaxMsgSizeBytes)}</td>
                  <td className="mono">
                    {item.MaxImageWidth} x {item.MaxImageHeight}
                  </td>
                  <td>
                    <div style={{ display: "grid", gap: 4 }}>
                      <span className="text-sm">IMG {asArray(item.AllowedImageTypes).length}</span>
                      <span className="text-sm">AUD {asArray(item.AllowedAudioTypes).length} / VID {asArray(item.AllowedVideoTypes).length}</span>
                    </div>
                  </td>
                  <td className="text-sm">{summarizeAdaptationClass(item)}</td>
                  <td>
                    <div className="flex gap-8">
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => openEdit(item)}>
                        Edit
                      </button>
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => void removeClass(item.Name)}>
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

      {showEditor ? (
        <Modal title={editing ? "Edit Adaptation Class" : "Add Adaptation Class"} onClose={() => { setShowEditor(false); setEditing(null); }} size="lg">
          <form onSubmit={submit}>
            <div className="modal-body surface-stack">
              <div className="surface-note">
                Keep visible policy text short. This class becomes runtime-selectable without service restart once saved to the database.
              </div>
              <div className="section-shell">
                <h2>Profile Identity And Size Limits</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Class Name</span>
                    <input className="input" value={form.Name} disabled={Boolean(editing)} onChange={(event) => setForm({ ...form, Name: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Max Message Size</span>
                    <input className="input" value={form.MaxMsgSizeBytes} onChange={(event) => setForm({ ...form, MaxMsgSizeBytes: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Max Image Width</span>
                    <input className="input" value={form.MaxImageWidth} onChange={(event) => setForm({ ...form, MaxImageWidth: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Max Image Height</span>
                    <input className="input" value={form.MaxImageHeight} onChange={(event) => setForm({ ...form, MaxImageHeight: event.target.value })} />
                  </label>
                </div>
              </div>
              <div className="section-shell">
                <h2>Allowed Media Types</h2>
                <div className="surface-grid">
                  <label className="field">
                    <span>Allowed Image Types</span>
                    <input className="input" value={form.AllowedImageTypes} onChange={(event) => setForm({ ...form, AllowedImageTypes: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Allowed Audio Types</span>
                    <input className="input" value={form.AllowedAudioTypes} onChange={(event) => setForm({ ...form, AllowedAudioTypes: event.target.value })} />
                  </label>
                  <label className="field">
                    <span>Allowed Video Types</span>
                    <input className="input" value={form.AllowedVideoTypes} onChange={(event) => setForm({ ...form, AllowedVideoTypes: event.target.value })} />
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
                {editing ? "Save Changes" : "Add Class"}
              </button>
            </div>
          </form>
        </Modal>
      ) : null}
    </div>
  );
}

function summarizeAdaptationClass(item: AdaptationClass): string {
  if (item.Name === "default") {
    return "Baseline handset policy";
  }
  return `${asArray(item.AllowedImageTypes).slice(0, 2).join(", ") || "No image types"}${asArray(item.AllowedImageTypes).length > 2 ? ", ..." : ""}`;
}

function formatConstraintBytes(value: number): string {
  if (!value) {
    return "0 B";
  }
  if (value >= 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(1)} MB`;
  }
  if (value >= 1024) {
    return `${Math.round(value / 1024)} KB`;
  }
  return `${value} B`;
}
