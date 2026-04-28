import { asArray, formatBytes, originLabel, RuntimeSnapshot, SMPPStatus, statusLabel, SystemStatus, useAPI } from "../lib/api";
import { StatCard } from "../components/StatCard";

export function Dashboard() {
  const messages = useAPI<{ messages: Array<{ Status: number; MessageSize: number; Direction: number; Origin: number }> }>(
    "/api/v1/messages?limit=100",
    { messages: [] },
    15000,
  );
  const runtime = useAPI<RuntimeSnapshot>("/api/v1/runtime", { peers: [], mm4_routes: [], mm3_relay: null, vasps: [], smpp_upstreams: [], adaptation: [] }, 15000);
  const smpp = useAPI<{ upstreams: SMPPStatus[] }>("/api/v1/smpp/status", { upstreams: [] }, 15000);
  const system = useAPI<SystemStatus>(
    "/api/v1/system/status",
    { version: "", uptime: "", started_at: "", queue_visible: 0, message_counts: {} },
    15000,
  );
  const messagesList = asArray(messages.data.messages);
  const peers = asArray(runtime.data.peers);
  const vasps = asArray(runtime.data.vasps);
  const smppUpstreams = asArray(smpp.data.upstreams);
  const mm3Enabled = Boolean(runtime.data.mm3_relay?.Enabled);
  const adaptationClasses = asArray(runtime.data.adaptation);

  const delivering = messagesList.filter((item) => item.Status === 1).length;
  const delivered = messagesList.filter((item) => item.Status === 2).length;
  const queued = messagesList.filter((item) => item.Status === 0).length;
  const activeQueue = queued + delivering;
  const totalBytes = messagesList.reduce((sum, item) => sum + item.MessageSize, 0);
  const bound = smppUpstreams.filter((item) => item.state === 2).length;
  const byOrigin = messagesList.reduce<Record<string, number>>((acc, item) => {
    const key = originLabel(item.Origin);
    acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});

  return (
    <div className="stack">
      {(messages.error || runtime.error || smpp.error || system.error) && (
        <div className="notice error">{messages.error || runtime.error || smpp.error || system.error}</div>
      )}
      <div className="stats-grid">
        <StatCard title="Messages" value={messagesList.length} subtitle="Recent slice" tooltip="Tracked MM1, MM3, MM4, and MM7 records in the current dashboard slice." />
        <StatCard title="Queue" value={activeQueue} subtitle="Queued + delivering" tooltip="Queued or actively delivering messages visible in the current slice." tone={activeQueue > 0 ? "warning" : "accent"} />
        <StatCard title="Delivered" value={delivered} subtitle="Final status" tooltip="Messages already in final delivered state." tone="success" />
        <StatCard title="Payload" value={formatBytes(totalBytes)} subtitle="Stored bytes" tooltip="Aggregate payload size across the current message slice." />
        <StatCard title="MM3 Relay" value={mm3Enabled ? "Online" : "Off"} subtitle="Email path" tooltip="Outbound MM3 relay state from the active runtime configuration." tone={mm3Enabled ? "success" : "danger"} />
        <StatCard title="SMPP Bound" value={bound} subtitle="Notification path" tooltip="Currently bound SMPP sessions used for handset MMS notifications." tone={bound > 0 ? "success" : "danger"} />
      </div>

      <div className="grid">
        <div className="panel">
          <h2>Traffic Mix</h2>
          <div className="list">
            {Object.entries(byOrigin).length > 0 ? (
              Object.entries(byOrigin).map(([origin, count]) => (
                <div className="list-item" key={origin}>
                  <strong>{origin}</strong>
                  <p>{count} recent records in the current slice.</p>
                </div>
              ))
            ) : (
              <div className="list-item">
                <strong>No Recent Traffic</strong>
                <p>No messages are visible in the current dashboard slice.</p>
              </div>
            )}
          </div>
        </div>

        <div className="panel">
          <h2>Recent Status</h2>
          <div className="list">
            {messagesList.slice(0, 5).map((item, index) => (
              <div className="list-item" key={`${item.Status}-${index}`}>
                <strong>{statusLabel(item.Status)}</strong>
                <p>
                  {originLabel(item.Origin)} • Direction {item.Direction === 0 ? "MO" : "MT"} • payload {formatBytes(item.MessageSize)}
                </p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
