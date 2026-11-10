import { describe, expect, it } from "vitest";
import { Scope } from "@/core/reactive";
import { settle, testContext } from "@/test/ctx";
import { snapshot } from "@/test/fixtures";
import timetable from "./index";

async function render(settings: Record<string, unknown> = {}) {
  const ctx = await testContext(snapshot(), [timetable], settings);
  const el = timetable.route!.render(ctx, new Scope(), new URLSearchParams({ date: "2026-10-07" }));
  await settle();
  return el;
}

describe("timetable view", () => {
  it("renders a working week with merged double lessons and parallel groups", async () => {
    const el = await render();
    expect(el.querySelectorAll(".tt-day")).toHaveLength(5);
    const cells = [...el.querySelectorAll<HTMLElement>(".tt-lesson")];
    expect(cells).toHaveLength(5); // the MATH double lesson is one cell
    const cs = cells.find((c) => c.textContent?.includes("CS"))!;
    expect(cs.style.width).toBe("calc(50% - 4px)");
    expect(el.querySelector(".st-cancelled")?.classList.contains("struck")).toBe(true);
    expect(el.querySelector(".st-exam .tt-badge")?.textContent).toBe("Exam");
  });

  it("follows a date change in the URL", async () => {
    history.replaceState(null, "", "#/timetable");
    const ctx = await testContext(snapshot(), [timetable]);
    const el = timetable.route!.render(ctx, new Scope(), new URLSearchParams({ date: "2026-10-07" }));
    await settle();
    const input = el.querySelector<HTMLInputElement>("input[type=date]")!;
    expect(input.value).toBe("2026-10-07");

    // history navigation changes the params without going through the toolbar
    ctx.router.setParams({ date: "2026-10-14" });
    await settle();
    expect(input.value).toBe("2026-10-14");
  });

  it("follows the settings", async () => {
    const el = await render({ timetable: { hideCancelled: true, mergeAdjacent: false, weekends: "always", showTeacher: false } });
    expect(el.querySelectorAll(".tt-day")).toHaveLength(7);
    expect(el.querySelectorAll(".tt-lesson")).toHaveLength(5);
    expect(el.textContent).not.toContain("AMB");
  });
});
