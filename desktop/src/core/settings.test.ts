import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "./api/client";
import { SettingsStore, getPath, patchFor } from "./settings";

/** RFC 7386 apply, the way the backend merges a settings patch. */
function mergePatch(doc: Record<string, unknown>, patch: Record<string, unknown>): Record<string, unknown> {
  const out = structuredClone(doc);
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) delete out[k];
    else if (v && typeof v === "object" && !Array.isArray(v))
      out[k] = mergePatch((out[k] as Record<string, unknown>) ?? {}, v as Record<string, unknown>);
    else out[k] = v;
  }
  return out;
}

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

  it("replaces a map so the keys it lost are deleted by the merge", async () => {
    vi.useFakeTimers();
    const { api, patches } = fakeApi({
      subjects: { custom: { "D.LK": { name: "Deutsch" }, MATH: { hidden: true } } },
    });
    const s = new SettingsStore(api);
    await s.load();
    s.setObject("subjects.custom", { "D.LK": { name: "Deutsch", color: "#00ff00" } });
    await vi.advanceTimersByTimeAsync(300);
    // MATH has to be nulled out: merging the map would keep it, and a dotted
    // code must stay one key rather than becoming { D: { LK: ... } }
    expect(patches).toEqual([
      { subjects: { custom: { MATH: null, "D.LK": { name: "Deutsch", color: "#00ff00" } } } },
    ]);
    expect(mergePatch({ subjects: { custom: { "D.LK": { name: "Deutsch" }, MATH: { hidden: true } } } }, patches[0] as Record<string, unknown>)).toEqual({
      subjects: { custom: { "D.LK": { name: "Deutsch", color: "#00ff00" } } },
    });
    vi.useRealTimers();
  });

  it("sends null for a field dropped inside a stored object", async () => {
    vi.useFakeTimers();
    // switching "hide" off removes the field from the override, which a merge
    // would otherwise never notice: the backend kept hidden: true
    const { api, patches } = fakeApi({ subjects: { custom: { MATH: { hidden: true, name: "Mathe" } } } });
    const s = new SettingsStore(api);
    await s.load();
    s.setObject("subjects.custom", { MATH: { name: "Mathe" } });
    await vi.advanceTimersByTimeAsync(300);
    expect(patches).toEqual([{ subjects: { custom: { MATH: { hidden: null, name: "Mathe" } } } }]);
    expect(mergePatch({ subjects: { custom: { MATH: { hidden: true, name: "Mathe" } } } }, patches[0] as Record<string, unknown>)).toEqual({
      subjects: { custom: { MATH: { name: "Mathe" } } },
    });
    vi.useRealTimers();
  });

  it("deletes every key when the map becomes empty", async () => {
    vi.useFakeTimers();
    const { api, patches } = fakeApi({ subjects: { custom: { "D.LK": { name: "Deutsch" }, MATH: { hidden: true } } } });
    const s = new SettingsStore(api);
    await s.load();
    s.setObject("subjects.custom", {});
    await vi.advanceTimersByTimeAsync(300);
    expect(patches).toEqual([{ subjects: { custom: { "D.LK": null, MATH: null } } }]);
    expect(mergePatch({ subjects: { custom: { "D.LK": { name: "Deutsch" }, MATH: { hidden: true } } } }, patches[0] as Record<string, unknown>)).toEqual({
      subjects: { custom: {} },
    });
    vi.useRealTimers();
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
