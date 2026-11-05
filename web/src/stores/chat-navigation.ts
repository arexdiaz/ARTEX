import { create } from "zustand";

type ChatNavigationState = {
  restoreRequest: number | null;
  requestRestore: () => void;
  completeRestore: (request: number) => void;
};

let nextRequest = 0;

// A bare Chat navigation explicitly asks for the saved session. Keep that
// intent across the route change: a static-export router cache can otherwise
// substitute the URL of the first conversation opened in this browser tab.
export const useChatNavigation = create<ChatNavigationState>((set) => ({
  restoreRequest: null,
  requestRestore: () => set({ restoreRequest: ++nextRequest }),
  completeRestore: (request) => set((state) => (
    state.restoreRequest === request ? { restoreRequest: null } : state
  )),
}));
