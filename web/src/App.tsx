import { Navigate, Route, Routes } from "react-router-dom";

import { Layout } from "./components/Layout";
import { Adaptation } from "./pages/Adaptation";
import { Dashboard } from "./pages/Dashboard";
import { Messages } from "./pages/Messages";
import { MM3 } from "./pages/MM3";
import { OAM } from "./pages/OAM";
import { Peers } from "./pages/Peers";
import { VASPs } from "./pages/VASPs";

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<Dashboard />} />
        <Route path="messages" element={<Messages />} />
        <Route path="peers" element={<Peers />} />
        <Route path="mm3" element={<MM3 />} />
        <Route path="vasps" element={<VASPs />} />
        <Route path="adaptation" element={<Adaptation />} />
        <Route path="runtime" element={<Navigate to="/oam" replace />} />
        <Route path="oam" element={<OAM />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Route>
    </Routes>
  );
}
