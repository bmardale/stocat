import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { createContext, useContext } from "react";
import { currentUserQueryOptions } from "@/api/current-user";
import { authLogout } from "@/api/generated/auth/auth";
import type { User } from "@/api/generated/model";

type AuthState = {
  user: User | null;
  setUser: (user: User) => void;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const queryClient = useQueryClient();
  const router = useRouter();
  const { data: user } = useSuspenseQuery(currentUserQueryOptions);

  const setUser = (next: User | null) => {
    queryClient.setQueryData(currentUserQueryOptions.queryKey, next);
  };

  const logout = async () => {
    await authLogout();
    setUser(null);
    await router.navigate({ to: "/login" });
  };

  return <AuthContext.Provider value={{ user, setUser, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);

  if (context === undefined) {
    throw new Error("useAuth must be used within an AuthProvider");
  }

  return context;
}
