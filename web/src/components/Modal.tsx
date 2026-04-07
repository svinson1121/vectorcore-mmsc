import { ReactNode, useEffect, useRef } from "react";

type ModalProps = {
  title: string;
  onClose: () => void;
  children: ReactNode;
  size?: "lg";
};

export function Modal({ title, onClose, children, size }: ModalProps) {
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const firstFocusRef = useRef<HTMLHeadingElement | null>(null);
  const onCloseRef = useRef(onClose);

  onCloseRef.current = onClose;

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    firstFocusRef.current?.focus();

    function handleKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        onCloseRef.current();
      }
    }

    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("keydown", handleKey);
      previous?.focus?.();
    };
  }, []);

  function handleOverlayClick(event: React.MouseEvent<HTMLDivElement>) {
    if (event.target === overlayRef.current) {
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
