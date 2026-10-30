import type { Host } from "../../types";
export interface PendingConnection {
  host: Host;
  name: string;
  stage: "connecting" | "trust" | "credentials" | "tmux" | "failed";
  messages: string[];
  error: string;
  fingerprint?: string;
  changedKey?: boolean;
  retry: () => void;
  trust?: () => void;
  credentials?: (secret: string, passphrase: string) => void;
  normal?: () => void;
}
