import { createContext, PropsWithChildren, useCallback, useContext, useEffect, useMemo, useState } from "react";

type ToastType = "success" | "error" | "warning" | "info";

type ToastEntry = {
  id: number;
  type: ToastType;
  title: string;
  message?: string;
};

type ToastAPI = {
  success: (title: string, message?: string) => void;
  error: (title: string, message?: string) => void;
  warning: (title: string, message?: string) => void;
  info: (title: string, message?: string) => void;
};

const ToastContext = createContext<ToastAPI | null>(null);

let nextToastID = 0;

const ICONS: Record<ToastType, string> = {
  success: "OK",
  error: "ER",
  warning: "!",
  info: "i",
};

function ToastItem({ id, type, title, message, onDismiss }: ToastEntry & { onDismiss: (id: number) => void }) {
  useEffect(() => {
    const timer = window.setTimeout(() => onDismiss(id), 4000);
    return () => window.clearTimeout(timer);
  }, [id, onDismiss]);

  return (
    <div className={`toast ${type}`} role="alert">
      <div className="toast-icon" aria-hidden="true">
        {ICONS[type]}
      </div>
      <div className="toast-body">
        <div className="toast-title">{title}</div>
        {message ? <div className="toast-message">{message}</div> : null}
      </div>
      <button className="toast-close" type="button" onClick={() => onDismiss(id)} aria-label="Dismiss">
        x
      </button>
    </div>
  );
}

export function ToastProvider({ children }: PropsWithChildren) {
  const [toasts, setToasts] = useState<ToastEntry[]>([]);

  const dismiss = useCallback((id: number) => {
    setToasts((current) => current.filter((item) => item.id !== id));
  }, []);

  const addToast = useCallback((type: ToastType, title: string, message?: string) => {
    const id = ++nextToastID;
    setToasts((current) => [...current.slice(-4), { id, type, title, message }]);
  }, []);

  const value = useMemo<ToastAPI>(
    () => ({
      success: (title, message) => addToast("success", title, message),
      error: (title, message) => addToast("error", title, message),
      warning: (title, message) => addToast("warning", title, message),
      info: (title, message) => addToast("info", title, message),
    }),
    [addToast],
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="toast-container">
        {toasts.map((toast) => (
          <ToastItem key={toast.id} {...toast} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastAPI {
  const value = useContext(ToastContext);
  if (!value) {
    throw new Error("useToast must be used within ToastProvider");
  }
  return value;
}
