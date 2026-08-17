import { Dispatch, SetStateAction, useCallback, useState } from "react";

// Drop-in replacement for useState that also reports whether the setter has
// ever been called by the user, so Add/Edit forms can track "has this been
// touched" without hand-rolling a dirty flag next to every field mutator.
//
// Form state here lives on the page component, not on a per-modal component
// that mounts fresh on each open, so openAdd/openEdit repopulate it via the
// same setter used by field edits. resetDirty lets those callers clear the
// flag after seeding initial values, so the dialog doesn't open already
// "dirty".
export function useDirtyState<T>(
  initialValue: T | (() => T),
): [T, Dispatch<SetStateAction<T>>, boolean, () => void] {
  const [state, setStateRaw] = useState<T>(initialValue);
  const [dirty, setDirty] = useState(false);

  const setState = useCallback<Dispatch<SetStateAction<T>>>((update) => {
    setDirty(true);
    setStateRaw(update);
  }, []);

  const resetDirty = useCallback(() => setDirty(false), []);

  return [state, setState, dirty, resetDirty];
}
