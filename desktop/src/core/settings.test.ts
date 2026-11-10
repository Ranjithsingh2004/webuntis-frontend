import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "./api/client";
import { SettingsStore, getPath, patchFor } from "./settings";

function fakeApi(initial: Record<string, unknown> = {}) {
  const patches: unknown[] = [];
  const api = {
    getSettings: vi.fn(async () => structuredClone(initial)),
    patchSettings: vi.fn(async (p: unknown) => {
      patches.push(p);
      return {};
    }),
  } as unknown as ApiClient;
  return { api, patches };
}

describe("path helpers", () => {
  it("reads nested values", () => {
    expect(getPath({ a: { b: 1 } }, "a.b")).toBe(1);
    expect(getPath({ a: 1 }, "a.b")).toBeUndefined();
  });
  it("builds merge patches", () => {
    expect(patchFor("a.b.c", 1)).toEqual({ a: { b: { c: 1 } } });
  });

  it("refuses a path that reaches the prototype", () => {
    // subject codes and other keys come from school data, so a path segment
    // like `__proto__` must never be followed
    expect(getPath({}, "__proto__.polluted")).toBeUndefined();
    expect(getPath({}, "constructor.prototype.polluted")).toBeUndefined();
    expect(patchFor("__proto__.polluted", 1)).toEqual({});
    expect(patchFor("constructor.prototype.polluted", 1)).toEqual({});
  });
});

describe("SettingsStore", () => {
  it("falls back to registered defaults", async () => {
    const { api } = fakeApi({ ui: { theme: "dark" } });
    const s = new SettingsStore(api);
    await s.load();
    s.registerDefault("ui.density", "cozy");
    expect(s.get("ui.theme")).toBe("dark");
    expect(s.get("ui.density")).toBe("cozy");
    expect(s.isSet("ui.density")).toBe(false);
  });

  it("applies writes locally and batches them into one patch", async () => {
    vi.useFakeTimers();
    const { api, patches } = fakeApi({ ui: { theme: "dark", accent: "#000" } });
    const s = new SettingsStore(api);
    await s.load();
    const seen = vi.fn();
    s.watch("ui.theme", seen);
    s.set("ui.theme", "light");
    s.reset("ui.accent");
    expect(s.get("ui.theme")).toBe("light");
    expect(s.isSet("ui.accent")).toBe(false);
    await vi.advanceTimersByTimeAsync(300);
    expect(patches).toEqual([{ ui: { theme: "light", accent: null } }]);
    expect(seen.mock.calls).toEqual([["dark"], ["light"]]);
    vi.useRealTimers();
  });
});
