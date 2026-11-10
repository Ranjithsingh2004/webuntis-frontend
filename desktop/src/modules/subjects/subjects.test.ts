import { describe, expect, it } from "vitest";
import { Scope } from "@/core/reactive";
import { settle, testContext } from "@/test/ctx";
import { lesson, snapshot } from "@/test/fixtures";
import timetable from "@/modules/timetable";
import subjects from "./index";
import { applySubjectOverride, removeSubjectOverride, subjectEditor, toHex } from "./editor";

describe("subject editor", () => {
  it("lists subjects from the data and stores overrides", async () => {
    const ctx = await testContext(snapshot(), [subjects]);
    await ctx.data.loadOverview();
    const el = subjectEditor(ctx, new Scope());
    expect([...el.querySelectorAll<HTMLElement>(".subject-row")].map((r) => r.dataset.code)).toEqual(["ART", "CS", "ENG", "GEO", "MATH"]);

    const row = el.querySelector('[data-code="MATH"]')!;
    const [name, , emoji] = row.querySelectorAll<HTMLInputElement>("input.input, input.color");
    name.value = "Maths";
    name.dispatchEvent(new Event("change"));
    emoji.value = "📐";
    emoji.dispatchEvent(new Event("change"));
    const hide = row.querySelector<HTMLInputElement>(".switch")!;
    hide.checked = true;
    hide.dispatchEvent(new Event("change"));
    expect(ctx.settings.get("subjects.custom")).toEqual({ MATH: { name: "Maths", emoji: "📐", hidden: true } });

    hide.checked = false;
    hide.dispatchEvent(new Event("change"));
    expect(ctx.settings.get("subjects.custom.MATH")).toEqual({ name: "Maths", emoji: "📐" });
    (row.querySelector(".icon-btn") as HTMLButtonElement).click();
    expect(ctx.settings.get("subjects.custom")).toEqual({});
  });

  it("converts auto colours for the colour picker", () => {
    expect(toHex("hsl(0 100% 50%)")).toBe("#ff0000");
    expect(toHex("#123456")).toBe("#123456");
  });
});

describe("subject overrides with a dotted code", () => {
  it("keeps the whole code as one key", () => {
    // `D.LK` written as the dotted path subjects.custom.D.LK became
    // { D: { LK: ... } }, which the display never found again
    expect(applySubjectOverride({}, "D.LK", { name: "Deutsch" })).toEqual({
      "D.LK": { name: "Deutsch" },
    });
  });

  it("merges into and clears a single override", () => {
    const start = { "D.LK": { name: "Deutsch", color: "#ff0000" } };
    expect(applySubjectOverride(start, "D.LK", { color: "" })).toEqual({
      "D.LK": { name: "Deutsch" },
    });
    expect(applySubjectOverride({ "D.LK": { name: "Deutsch" } }, "D.LK", { name: "" })).toEqual({});
  });

  it("removes one code without touching a code that prefixes it", () => {
    expect(removeSubjectOverride({ D: { name: "Dot" }, "D.LK": { name: "Deutsch" } }, "D.LK")).toEqual({
      D: { name: "Dot" },
    });
  });

  it("is applied by the display once stored in settings", async () => {
    const ctx = await testContext(snapshot(), [subjects]);
    ctx.settings.set("subjects.custom", { "D.LK": { name: "Deutsch", hidden: false } });
    const replaced = lesson("2026-10-07", "08:00", "08:50", "D.LK");
    expect(ctx.display.subject(replaced)).toBe("Deutsch");
  });
});

describe("filters in views", () => {
  it("the timetable redraws when a filter changes", async () => {
    const ctx = await testContext(snapshot(), [subjects, timetable]);
    const el = timetable.route!.render(ctx, new Scope(), new URLSearchParams({ date: "2026-10-07" }));
    await settle();
    const before = el.querySelectorAll(".tt-lesson").length;
    ctx.settings.set("filters.mode", "except");
    ctx.settings.set("filters.subjects", ["MATH"]);
    expect(el.querySelectorAll(".tt-lesson").length).toBe(before - 1);
    expect(el.textContent).not.toContain("MATH");
  });
});
