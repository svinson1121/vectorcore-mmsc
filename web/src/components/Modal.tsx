import { ReactNode, useEffect, useRef } from "react";

type ModalProps = {
  title: string;
  onClose: () => void;
  children: ReactNode;
  size?: "lg";
  closeOnBackdrop?: boolean;
  closeOnEscape?: boolean;
};

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
}

export function Modal({ title, onClose, children, size, closeOnBackdrop = true, closeOnEscape = true }: ModalProps) {
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const firstFocusRef = useRef<HTMLHeadingElement | null>(null);
  const onCloseRef = useRef(onClose);
  const closeOnEscapeRef = useRef(closeOnEscape);

  onCloseRef.current = onClose;
  closeOnEscapeRef.current = closeOnEscape;

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    firstFocusRef.current?.focus();

    function handleKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        if (closeOnEscapeRef.current) {
          onCloseRef.current();
        }
        return;
      }
      // The browser's legacy "Backspace navigates back" behavior fires
      // whenever focus sits on a non-editable element (e.g. this dialog's
      // own title, focused on open) — silently unmounting the dialog via
      // history navigation. Suppress it outside editable controls so
      // Backspace only ever does its normal thing inside inputs.
      if (event.key === "Backspace" && !isEditableTarget(event.target)) {
        event.preventDefault();
      }
    }

    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("keydown", handleKey);
      previous?.focus?.();
    };
  }, []);

  function handleOverlayClick(event: React.MouseEvent<HTMLDivElement>) {
    if (event.target === overlayRef.current && closeOnBackdrop) {
      onCloseRef.current();
    }
  }

  return (
    <div className="modal-overlay" ref={overlayRef} onClick={handleOverlayClick} role="dialog" aria-modal="true">
      <div className={`modal ${size ? `modal-${size}` : ""}`.trim()} role="document">
        <div className="modal-header">
          <h3 className="modal-title" ref={firstFocusRef} tabIndex={-1}>
            {title}
          </h3>
          <button className="btn-icon" type="button" onClick={onClose} aria-label="Close modal">
            x
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
