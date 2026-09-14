import { createContext, useContext, useMemo, useState } from "react";
import { useAuth } from "@/components/auth-provider";
import { lockAccount, type UnlockedAccount } from "@/lib/v2/account-crypto";

type AccountEncryptionState = {
  account?: UnlockedAccount;
  unlock: (account: UnlockedAccount) => void;
  lock: () => void;
};

const AccountEncryptionContext = createContext<AccountEncryptionState | undefined>(undefined);

// Keys stay in memory only. A reload, a sign-out, or another account locks encryption.
export function AccountEncryptionProvider({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  const [unlocked, setUnlocked] = useState<UnlockedAccount>();
  const account = unlocked && unlocked.accountId === user?.id ? unlocked : undefined;

  const value = useMemo<AccountEncryptionState>(
    () => ({
      account,
      unlock: (next) =>
        setUnlocked((current) => {
          if (current && current !== next) lockAccount(current);
          return next;
        }),
      lock: () =>
        setUnlocked((current) => {
          if (current) lockAccount(current);
          return undefined;
        }),
    }),
    [account],
  );

  return (
    <AccountEncryptionContext.Provider value={value}>{children}</AccountEncryptionContext.Provider>
  );
}

export function useAccountEncryption() {
  const context = useContext(AccountEncryptionContext);

  if (context === undefined) {
    throw new Error("useAccountEncryption must be used within an AccountEncryptionProvider");
  }

  return context;
}
