import { useState } from "react";

import { Modal } from "../components/Modal";
import { asArray, RuntimeSnapshot, useAPI } from "../lib/api";

export function ConfigPage() {
  const runtime = useAPI<RuntimeSnapshot>(
    "/api/v1/runtime",
    { peers: [], vasps: [], mm3_relay: null, smpp_upstreams: [], adaptation: [] },
    15000,
  );
  const [snapshotOpen, setSnapshotOpen] = useState(false);

  const peers = asArray(runtime.data.peers);
  const vasps = asArray(runtime.data.vasps);
  const upstreams = asArray(runtime.data.smpp_upstreams);
  const adaptation = asArray(runtime.data.adaptation);
  const mm3Relay = runtime.data.mm3_relay;

  const activePeers = peers.filter((item) => item.Active).length;
  const activeVASPs = vasps.filter((item) => item.Active).length;
  const activeUpstreams = upstreams.filter((item) => item.Active).length;
  const relayReady = Boolean(mm3Relay?.Enabled && mm3Relay?.SMTPHost);
  const defaultClassPresent = adaptation.some((item) => item.Name === "default");
  const warnings = buildRuntimeWarnings({
    activePeers,
    activeVASPs,
    activeUpstreams,
    relayReady,
    defaultClassPresent,
  });

  return (
    <div className="stack">
      {runtime.error && <div className="notice error">{runtime.error}</div>}

      <div className="grid">
        <div className="card summary-card">
          <span className="pill">MM4 Runtime</span>
          <strong>{activePeers}/{peers.length}</strong>
          <div className="summary-card-copy">Active peer routes loaded into the live runtime snapshot.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">MM7 Runtime</span>
          <strong>{activeVASPs}/{vasps.length}</strong>
          <div className="summary-card-copy">Active SOAP or EAIF VASPs in the live runtime snapshot.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">MM3 Runtime</span>
          <strong>{relayReady ? "Ready" : "Partial"}</strong>
          <div className="summary-card-copy">{relayReady ? mm3Relay?.SMTPHost || "relay configured" : "Relay disabled or missing SMTP target."}</div>
        </div>
        <div className="card summary-card">
          <span className="pill">SMPP Runtime</span>
          <strong>{activeUpstreams}/{upstreams.length}</strong>
          <div className="summary-card-copy">Configured upstreams available to the live SMPP manager.</div>
        </div>
        <div className="card summary-card">
          <span className="pill">Adaptation</span>
          <strong>{adaptation.length}</strong>
          <div className="summary-card-copy">Constraint classes visible to the delivery pipeline right now.</div>
        </div>
      </div>

      <div className="section-shell">
        <div className="flex justify-between mb-12">
          <div>
            <h2>Runtime Coverage</h2>
            <div className="text-muted text-sm">Live mutable configuration after database-backed reload and apply.</div>
          </div>
          <div className="flex gap-8">
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => void runtime.reload()}>
              Refresh
            </button>
            <button className="btn btn-primary btn-sm" type="button" onClick={() => setSnapshotOpen(true)}>
              Runtime Snapshot
            </button>
          </div>
        </div>

        <div className="surface-stack">
          {warnings.length > 0 ? (
            <div className="surface-note">
              {warnings.map((warning) => (
                <div key={warning}>{warning}</div>
              ))}
            </div>
          ) : (
            <div className="surface-note">No immediate runtime coverage gaps are visible from the current mutable configuration snapshot.</div>
          )}

          <div className="split">
            <div className="section-shell">
              <h2>Transport Runtime</h2>
              <div className="table-wrap">
                <table>
                  <tbody>
                    <tr>
                      <td><strong>MM4 Peers</strong></td>
                      <td>{activePeers} active / {peers.length} total</td>
                    </tr>
                    <tr>
                      <td><strong>MM3 Relay</strong></td>
                      <td>{relayReady ? `Ready via ${mm3Relay?.SMTPHost}:${mm3Relay?.SMTPPort}` : "Not ready"}</td>
                    </tr>
                    <tr>
                      <td><strong>SMPP Upstreams</strong></td>
                      <td>{activeUpstreams} active / {upstreams.length} total</td>
                    </tr>
                    <tr>
                      <td><strong>Adaptation Default</strong></td>
                      <td>{defaultClassPresent ? "Present" : "Missing"}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div className="section-shell">
              <h2>Partner Runtime</h2>
              <div className="table-wrap">
                <table>
                  <tbody>
                    <tr>
                      <td><strong>MM7 VASPs</strong></td>
                      <td>{activeVASPs} active / {vasps.length} total</td>
                    </tr>
                    <tr>
                      <td><strong>EAIF Endpoints</strong></td>
                      <td>{vasps.filter((item) => (item.Protocol || "soap") === "eaif").length}</td>
                    </tr>
                    <tr>
                      <td><strong>SOAP Endpoints</strong></td>
                      <td>{vasps.filter((item) => (item.Protocol || "soap") !== "eaif").length}</td>
                    </tr>
                    <tr>
                      <td><strong>Adaptation Classes</strong></td>
                      <td>{adaptation.length} visible to runtime</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </div>

      {snapshotOpen ? (
        <Modal title="Runtime Snapshot" onClose={() => setSnapshotOpen(false)} size="lg">
          <div className="modal-body">
            <pre className="code">{JSON.stringify(runtime.data, null, 2)}</pre>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}

function buildRuntimeWarnings(input: {
  activePeers: number;
  activeVASPs: number;
  activeUpstreams: number;
  relayReady: boolean;
  defaultClassPresent: boolean;
}): string[] {
  const warnings: string[] = [];
  if (input.activePeers === 0) {
    warnings.push("No active MM4 peers are loaded, so inter-MMSC forwarding is effectively offline.");
  }
  if (input.activeUpstreams === 0) {
    warnings.push("No active SMPP upstreams are loaded, so handset WAP Push delivery is effectively offline.");
  }
  if (!input.relayReady) {
    warnings.push("MM3 relay is not runtime-ready, so MMS-to-email relay is unavailable until relay settings are completed.");
  }
  if (input.activeVASPs === 0) {
    warnings.push("No active MM7 VASPs are loaded, so VASP-facing MM7 traffic is not currently provisioned.");
  }
  if (!input.defaultClassPresent) {
    warnings.push("The default adaptation class is missing, so adaptation policy may fall back inconsistently.");
  }
  return warnings;
}
