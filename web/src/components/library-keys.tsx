import { createContext, useContext, useMemo, useState } from "react";
import type { LibraryKeys } from "@/lib/library-crypto";

type LibraryKeysState = {
  keys: (libraryId: string) => LibraryKeys | undefined;
  unlock: (libraryId: string, keys: LibraryKeys) => void;
  lock: (libraryId: string) => void;
};

const LibraryKeysContext = createContext<LibraryKeysState | undefined>(undefined);

// Keys stay in memory only. A reload or a sign-out locks all libraries.
export function LibraryKeysProvider({ children }: { children: React.ReactNode }) {
  const [unlocked, setUnlocked] = useState<Record<string, LibraryKeys>>({});

  // Consumers use the value as an effect dependency. Keep it stable until the keys change.
  const value = useMemo<LibraryKeysState>(
    () => ({
      keys: (libraryId) => unlocked[libraryId],
      unlock: (libraryId, keys) => setUnlocked((current) => ({ ...current, [libraryId]: keys })),
      lock: (libraryId) =>
        setUnlocked((current) => {
          const next = { ...current };
          delete next[libraryId];
          return next;
        }),
    }),
    [unlocked],
  );

  return <LibraryKeysContext.Provider value={value}>{children}</LibraryKeysContext.Provider>;
}

export function useLibraryKeys() {
  const context = useContext(LibraryKeysContext);

  if (context === undefined) {
    throw new Error("useLibraryKeys must be used within a LibraryKeysProvider");
  }

  return context;
}
