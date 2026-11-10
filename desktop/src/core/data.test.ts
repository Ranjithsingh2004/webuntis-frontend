import { describe, expect, it, vi } from "vitest";
import { ApiError, type ApiClient } from "./api/client";
import type { BackendEvent } from "./api/types";
import { createBus } from "./app";
import { DataService } from "./data";
import { snapshot } from "@/test/fixtures";

function setup(over: Partial<Record<keyof ApiClient, unknown>> = {}) {
  let emit: (ev: BackendEvent) => void = () => {};
  const api = {
    overview: vi.fn(async () => snapshot()),
    status: vi.fn(async () => ({ version: "t" })),
    snapshot: vi.fn(async () => snapshot()),
    refresh: vi.fn(async () => ({ snapshot: snapshot(), changes: [{ kind: "cancelled" }] })),
    events: vi.fn((fn: (ev: BackendEvent) => void, state: (c: boolean) => void) => {
      emit = fn;
      state(true);
      return () => {};
    }),
    ...over,
  } as unknown as ApiClient;
  const bus = createBus();
  const data = new DataService(api, bus);
  return { api, bus, data, emit: (ev: BackendEvent) => emit(ev) };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

describe("DataService", () => {
  it("loads overview and status on start and tracks the connection", async () => {
    const { data } = setup();
    data.start();
    await tick();
    expect(data.overview.get()?.lessons.length).toBeGreaterThan(0);
    expect(data.status.get()?.version).toBe("t");
    expect(data.connected.get()).toBe(true);
  });

  it("caches windows until the next snapshot event", async () => {
    const { api, data, emit } = setup();
    data.start();
    await data.window("2026-10-05", "2026-10-11");
    await data.window("2026-10-05", "2026-10-11");
    expect(api.snapshot).toHaveBeenCalledTimes(1);
    const rev = data.revision.get();
    emit({ type: "snapshot", at: "" });
    expect(data.revision.get()).toBe(rev + 1);
    await data.window("2026-10-05", "2026-10-11");
    expect(api.snapshot).toHaveBeenCalledTimes(2);
  });

  it("reloads the service status after a background refresh", async () => {
    // the status page shows lastRefresh/nextRefresh/lastError, which a
    // background refresh changes without any other event
    let st = { version: "t", service: { lastRefresh: "2026-10-07T08:00:00Z" } };
    const { api, data, emit } = setup({ status: vi.fn(async () => structuredClone(st)) });
    data.start();
    await tick();
    expect(data.status.get()?.service.lastRefresh).toBe("2026-10-07T08:00:00Z");

    st = { version: "t", service: { lastRefresh: "2026-10-07T09:00:00Z" } };
    emit({ type: "snapshot", at: "" });
    await tick();
    expect(api.status).toHaveBeenCalledTimes(2);
    expect(data.status.get()?.service.lastRefresh).toBe("2026-10-07T09:00:00Z");
  });

  it("forwards change events to the bus", () => {
    const { bus, data, emit } = setup();
    const seen = vi.fn();
    bus.on("changes", seen);
    data.start();
    emit({ type: "changes", at: "", data: [{ kind: "cancelled" }] });
    expect(seen).toHaveBeenCalledWith([{ kind: "cancelled" }]);
  });

  it("keeps errors as ErrorInfo and clears them on success", async () => {
    let fail = true;
    const { data } = setup({
      overview: vi.fn(async () => {
        if (fail) throw new ApiError("login", "bad password", 403);
        return snapshot();
      }),
    });
    await data.loadOverview();
    expect(data.error.get()).toEqual({ kind: "login", message: "bad password" });
    fail = false;
    await data.loadOverview();
    expect(data.error.get()).toBeNull();
  });

  it("refresh returns the detected changes", async () => {
    const { data } = setup();
    expect(await data.refresh()).toEqual([{ kind: "cancelled" }]);
    expect(data.loading.get()).toBe(false);
  });
});
