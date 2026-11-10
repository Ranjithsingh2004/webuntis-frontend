import { ApiError, type ApiClient } from "./api/client";
import type { Change, ErrorInfo, Snapshot, Status } from "./api/types";
import type { Bus } from "./bus";
import { Signal, type ReadSignal } from "./reactive";
import type { AppEvents } from "./app";

/**
 * Holds the data the views render: the background-refreshed overview
 * (dashboard and list views) and on-demand windows (timetable weeks).
 */
export class DataService {
  private readonly overviewSig = new Signal<Snapshot | null>(null);
  private readonly statusSig = new Signal<Status | null>(null);
  private readonly loadingSig = new Signal(false);
  private readonly errorSig = new Signal<ErrorInfo | null>(null);
  private readonly connectedSig = new Signal(false);
  private readonly windows = new Map<string, Promise<Snapshot>>();
  /** Bumped whenever cached windows become stale; views re-query on it. */
  private readonly revisionSig = new Signal(0);

  constructor(
    private readonly api: ApiClient,
    private readonly bus: Bus<AppEvents>,
  ) {}

  get overview(): ReadSignal<Snapshot | null> {
    return this.overviewSig;
  }
  get status(): ReadSignal<Status | null> {
    return this.statusSig;
  }
  get loading(): ReadSignal<boolean> {
    return this.loadingSig;
  }
  get error(): ReadSignal<ErrorInfo | null> {
    return this.errorSig;
  }
  get connected(): ReadSignal<boolean> {
    return this.connectedSig;
  }
  get revision(): ReadSignal<number> {
    return this.revisionSig;
  }

  /** Starts listening to backend events and loads the first data. */
  start(): () => void {
    const stop = this.api.events(
      (ev) => {
        switch (ev.type) {
          case "snapshot":
            // Published after every successful background refresh, which also
            // moves lastRefresh/nextRefresh and clears lastError.
            this.invalidate();
            void this.loadOverview();
            void this.loadStatus();
            break;
          case "changes":
            this.bus.emit("changes", (ev.data as Change[]) ?? []);
            break;
          case "error":
            this.errorSig.set(ev.data as ErrorInfo);
            void this.loadStatus();
            break;
          case "provider":
            this.invalidate();
            this.overviewSig.set(null);
            void this.loadStatus();
            break;
          case "settings":
            this.bus.emit("settings-changed", undefined);
            break;
        }
      },
      (connected) => this.connectedSig.set(connected),
    );
    void this.loadOverview();
    void this.loadStatus();
    return stop;
  }

  private invalidate(): void {
    this.windows.clear();
    this.revisionSig.update((n) => n + 1);
  }

  async loadStatus(): Promise<void> {
    try {
      this.statusSig.set(await this.api.status());
    } catch {
      /* status is informative only */
    }
  }

  async loadOverview(fresh = false): Promise<void> {
    this.loadingSig.set(true);
    try {
      this.overviewSig.set(await this.api.overview(fresh));
      this.errorSig.set(null);
    } catch (err) {
      this.errorSig.set(toErrorInfo(err));
    } finally {
      this.loadingSig.set(false);
    }
  }

  /** Refetches everything now (the refresh button). */
  async refresh(): Promise<Change[]> {
    this.loadingSig.set(true);
    try {
      const res = await this.api.refresh();
      this.invalidate();
      this.overviewSig.set(res.snapshot);
      this.errorSig.set(null);
      void this.loadStatus();
      return res.changes;
    } catch (err) {
      this.errorSig.set(toErrorInfo(err));
      throw err;
    } finally {
      this.loadingSig.set(false);
    }
  }

  /** Data for an arbitrary window, cached until the next refresh. */
  window(from: string, to: string): Promise<Snapshot> {
    const key = `${from}..${to}`;
    let p = this.windows.get(key);
    if (!p) {
      p = this.api.snapshot(from, to);
      this.windows.set(key, p);
      p.catch(() => this.windows.delete(key));
    }
    return p;
  }

  who(code: string) {
    return this.api.who(code);
  }
}

export function toErrorInfo(err: unknown): ErrorInfo {
  if (err instanceof ApiError) return { kind: err.kind, message: err.message };
  return { kind: "internal", message: err instanceof Error ? err.message : String(err) };
}
