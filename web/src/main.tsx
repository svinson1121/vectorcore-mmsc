import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
import { ToastProvider } from "./components/Toast";
import "./styles.css";
import { ThemeProvider } from "./theme";

class ErrorBoundary extends React.Component<React.PropsWithChildren, { error: string }> {
  constructor(props: React.PropsWithChildren) {
    super(props);
    this.state = { error: "" };
  }

  static getDerivedStateFromError(error: Error) {
    return { error: error.message || "frontend render failed" };
  }

  render() {
    if (this.state.error) {
      return (
        <div className="shell">
          <main className="content">
            <section className="panel">
              <h2>UI Error</h2>
              <div className="notice error">{this.state.error}</div>
            </section>
          </main>
        </div>
      );
    }
    return this.props.children;
  }
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ThemeProvider>
      <ToastProvider>
        <BrowserRouter>
          <ErrorBoundary>
            <App />
          </ErrorBoundary>
        </BrowserRouter>
      </ToastProvider>
    </ThemeProvider>
  </React.StrictMode>,
);
