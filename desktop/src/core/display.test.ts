import { describe, expect, it } from "vitest";
import { Display, autoColor, elementName } from "./display";
import { testContext } from "@/test/ctx";
import { lesson, snapshot } from "@/test/fixtures";
import subjects from "@/modules/subjects";

async function display(settings: Record<string, unknown> = {}) {
  const ctx = await testContext(snapshot(), [subjects], settings);
  return ctx.display;
}

const math = lesson("2026-10-05", "08:00", "08:50", "MATH");
const event = lesson("2026-10-05", "10:00", "10:50", "X", { subjects: [], event: true, status: "event", info: "Museum" });
const cancelled = lesson("2026-10-05", "09:00", "09:50", "ENG", { cancelled: true, status: "cancelled" });

describe("names", () => {
  it("formats elements in every mode", () => {
    const el = { short: "MATH", long: "Mathematics" };
    expect(elementName(el, "short")).toBe("MATH");
    expect(elementName(el, "long")).toBe("Mathematics");
    expect(elementName(el, "both")).toBe("Mathematics (MATH)");
    expect(elementName({ short: "R1" }, "long")).toBe("R1");
  });

  it("applies name modes and overrides", async () => {
    const d = await display({ ui: { names: { subjects: "long", teachers: "both" } }, subjects: { custom: { MATH: { name: "Maths" } } } });
    expect(d.subject(math)).toBe("Maths");
    expect(d.subject(cancelled)).toBe("ENG long");
    expect(d.subject(event)).toBe("Museum");
    expect(d.teachers(math.teachers)).toBe("Ms. Ambrose (AMB)");
  });
});

describe("filters", () => {
  const lessons = [math, event, cancelled];
  const subjectsOf = (ls: typeof lessons) => ls.map((l) => l.subjects[0]?.short ?? "event");

  it("only keeps listed subjects and drops events", async () => {
    const d = await display({ filters: { mode: "only", subjects: ["math LONG"] } }); // long name, any case
    expect(subjectsOf(d.visibleLessons(lessons))).toEqual(["MATH"]);
  });

  it("except hides listed subjects but keeps events", async () => {
    const d = await display({ filters: { mode: "except", subjects: ["math"] } });
    expect(subjectsOf(d.visibleLessons(lessons))).toEqual(["event", "ENG"]);
  });

  it("hides hidden subjects, cancelled and removed lessons", async () => {
    const d = await display({ subjects: { custom: { MATH: { hidden: true } } }, filters: { hideCancelled: true } });
    expect(subjectsOf(d.visibleLessons(lessons))).toEqual(["event"]);
    expect(d.visibleExams([{ id: "1", date: "", subjects: [{ short: "MATH" }], teachers: [], rooms: [] }])).toEqual([]);
  });
});

describe("subjectStyle", () => {
  it("keeps status colours unless asked to colour by subject", async () => {
    const d = await display({ subjects: { custom: { MATH: { color: "#ff0000", emoji: "📐" }, ENG: { color: "#00ff00" } } } });
    expect(d.subjectStyle(math)).toEqual({ color: "#ff0000", emoji: "📐" });
    expect(d.subjectStyle(cancelled).color).toBeUndefined();
    expect(d.subjectStyle(event)).toEqual({});
  });

  it("generates colours in subject mode and colours everything in always mode", async () => {
    const d = await display({ subjects: { colorMode: "subject" } });
    expect(d.subjectStyle(math).color).toBe(autoColor("MATH"));
    expect(d.subjectStyle(cancelled).color).toBeUndefined();
    const always = await display({ subjects: { colorMode: "always" } });
    expect(always.subjectStyle(cancelled).color).toBe(autoColor("ENG"));
  });

  it("auto colours are stable and distinct", () => {
    expect(autoColor("MATH")).toBe(autoColor("MATH"));
    expect(autoColor("MATH")).not.toBe(autoColor("ENG"));
  });
});

describe("collectSubjects", () => {
  it("dedupes and prefers entries with long names", () => {
    const got = Display.collectSubjects([[{ subjects: [{ short: "B" }, { short: "A" }] }], [{ subjects: [{ short: "B", long: "Bee" }] }]]);
    expect(got).toEqual([{ short: "A", long: undefined }, { short: "B", long: "Bee" }]);
  });
});

describe("replaced subjects", () => {
  // WebUntis keeps the replaced subject on the lesson with status REMOVED and
  // puts the subject that replaced it beside it, so `REL` becomes `MATH` here.
  const replaced = lesson("2026-10-07", "08:00", "08:50", "REL", {
    subjects: [{ short: "REL", status: "REMOVED" }, { short: "MATH" }],
    substitution: true,
  });

  it("keeps the lesson when the replaced subject is hidden", async () => {
    const d = await display({ subjects: { custom: { REL: { hidden: true } } } });
    expect(d.visibleLessons([replaced])).toHaveLength(1);
    expect(d.subject(replaced)).toBe("MATH");
  });

  it("does not match filters through the replaced subject", async () => {
    const onlyRel = await display({ filters: { mode: "only", subjects: ["REL"] } });
    expect(onlyRel.visibleLessons([replaced])).toEqual([]);
    const onlyMath = await display({ filters: { mode: "only", subjects: ["MATH"] } });
    expect(onlyMath.visibleLessons([replaced])).toHaveLength(1);
    const exceptRel = await display({ filters: { mode: "except", subjects: ["REL"] } });
    expect(exceptRel.visibleLessons([replaced])).toHaveLength(1);
  });

  it("styles the lesson by the subject that replaced it", async () => {
    const d = await display({ subjects: { custom: { REL: { color: "#ff0000", emoji: "🕊" }, MATH: { color: "#0000ff", emoji: "📐" } } } });
    expect(d.subjectStyle(replaced)).toEqual({ color: "#0000ff", emoji: "📐" });
  });

  it("keeps the lesson hidden when every subject on it is removed", async () => {
    const allRemoved = lesson("2026-10-07", "09:00", "09:50", "REL", { subjects: [{ short: "REL", status: "REMOVED" }] });
    const d = await display({ subjects: { custom: { REL: { hidden: true } } } });
    expect(d.visibleLessons([allRemoved])).toEqual([]);
  });

  it("matches filters on the only subject there is when all are removed", async () => {
    const allRemoved = lesson("2026-10-07", "09:00", "09:50", "REL", { subjects: [{ short: "REL", status: "REMOVED" }] });
    const onlyRel = await display({ filters: { mode: "only", subjects: ["REL"] } });
    expect(onlyRel.visibleLessons([allRemoved])).toHaveLength(1);
    const exceptRel = await display({ filters: { mode: "except", subjects: ["REL"] } });
    expect(exceptRel.visibleLessons([allRemoved])).toEqual([]);
  });

  it("falls back to the first subject when every subject is removed", async () => {
    const allRemoved = lesson("2026-10-07", "09:00", "09:50", "REL", { subjects: [{ short: "REL", status: "REMOVED" }] });
    const d = await display({ subjects: { custom: { REL: { color: "#ff0000" } } } });
    expect(d.subjectStyle(allRemoved).color).toBe("#ff0000");
  });
});
