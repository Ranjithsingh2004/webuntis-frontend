import type { Element, Exam, Homework, Lesson } from "./api/types";
import type { SettingsStore } from "./settings";

/**
 * How codes are shown to the user. Every view goes through this class,
 * so naming (short codes, full names or both), per-subject overrides and
 * subject filters behave the same everywhere.
 *
 * The settings it reads are declared by the subjects module.
 */
export type NameMode = "short" | "long" | "both";

/** User overrides for one subject, keyed by its short code. */
export interface SubjectOverride {
  name?: string;
  color?: string;
  emoji?: string;
  hidden?: boolean;
}

export interface SubjectStyle {
  color?: string;
  emoji?: string;
}

export function elementName(el: Element, mode: NameMode): string {
  if (mode === "short" || !el.long) return el.short;
  if (mode === "long") return el.long;
  return `${el.long} (${el.short})`;
}

export function names(els: Element[], mode: NameMode, sep = ", "): string {
  return els.filter((e) => e.status !== "REMOVED").map((e) => elementName(e, mode)).join(sep);
}

/** A stable, pleasant colour for a subject code (used when subjects are
 * coloured by subject and the user picked no colour). */
export function autoColor(code: string): string {
  let hash = 0;
  for (const c of code) hash = (hash * 31 + c.charCodeAt(0)) >>> 0;
  return `hsl(${hash % 360} 62% 48%)`;
}

/** Does a subject match a filter entry (short or long name, any case)? */
function matchesEntry(el: Element, entry: string): boolean {
  const e = entry.trim().toLowerCase();
  return el.short.toLowerCase() === e || (el.long ?? "").toLowerCase() === e;
}

export class Display {
  constructor(private readonly settings: SettingsStore) {}

  private mode(kind: "subjects" | "teachers" | "rooms"): NameMode {
    return this.settings.get<NameMode>(`ui.names.${kind}`, "short");
  }

  override(code: string): SubjectOverride {
    return this.settings.get<Record<string, SubjectOverride>>("subjects.custom", {})[code] ?? {};
  }

  /** One subject element, with the user's name override applied. */
  subjectName(el: Element): string {
    return this.override(el.short).name?.trim() || elementName(el, this.mode("subjects"));
  }

  subject(l: Pick<Lesson, "subjects"> & { info?: string }): string {
    const subjects = l.subjects.filter((s) => s.status !== "REMOVED");
    if (!subjects.length) return l.info || "Event";
    return subjects.map((s) => this.subjectName(s)).join(" / ");
  }

  teachers(els: Element[]): string {
    return names(els, this.mode("teachers"));
  }

  rooms(els: Element[]): string {
    return names(els, this.mode("rooms"));
  }

  /** Whether a subject list passes the hidden flags and only/except filter. */
  private keep(subjects: Element[]): boolean {
    // WebUntis marks a replaced subject as REMOVED and adds the new one beside
    // it. Only the subjects actually being taught decide visibility, otherwise
    // hiding the subject that was replaced hides the lesson that replaced it.
    // A lesson whose subjects are all removed has nothing that replaced them,
    // so it falls back to all of them: hiding that subject still hides the
    // lesson, as it did before the replaced subject was filtered out.
    const active = subjects.filter((s) => s.status !== "REMOVED");
    const shown = active.length ? active : subjects;
    if (shown.some((s) => this.override(s.short).hidden)) return false;
    const mode = this.settings.get<string>("filters.mode", "none");
    const entries = this.settings.get<string[]>("filters.subjects", []);
    if (mode === "none" || !entries.length) return true;
    const hit = shown.some((s) => entries.some((e) => matchesEntry(s, e)));
    // Like `untis --only/--except`: events have no subject, so --only
    // hides them and --except keeps them.
    return mode === "only" ? hit : !hit;
  }

  /** Lessons the user wants to see. */
  visibleLessons(lessons: Lesson[]): Lesson[] {
    const hideCancelled = this.settings.get<boolean>("filters.hideCancelled", false);
    const hideRemoved = this.settings.get<boolean>("filters.hideRemoved", false);
    return lessons.filter((l) => this.keep(l.subjects) && !(hideCancelled && l.cancelled) && !(hideRemoved && l.removed));
  }

  visibleExams(exams: Exam[]): Exam[] {
    return exams.filter((e) => this.keep(e.subjects));
  }

  visibleHomework(items: Homework[]): Homework[] {
    return items.filter((h) => this.keep(h.subjects));
  }

  /**
   * Per-subject look. The colour applies to regular lessons (so states
   * like "cancelled" keep their meaning), and to everything when the
   * user chose to colour by subject everywhere.
   */
  subjectStyle(l: Pick<Lesson, "subjects"> & { status?: Lesson["status"] }): SubjectStyle {
    // The replaced subject stays first in the list, so colouring by
    // `subjects[0]` paints a substituted lesson in the colour and emoji of the
    // subject it no longer is. Use the first subject still being taught.
    const first = l.subjects.find((s) => s.status !== "REMOVED") ?? l.subjects[0];
    if (!first) return {};
    const o = this.override(first.short);
    const colorMode = this.settings.get<string>("subjects.colorMode", "status");
    const regular = !l.status || l.status === "regular";
    let color: string | undefined;
    if (colorMode !== "status" && (regular || colorMode === "always")) color = o.color || autoColor(first.short);
    else if (regular && o.color) color = o.color;
    return { color, emoji: o.emoji || undefined };
  }

  /** Every subject code seen in the given data, with its long name. */
  static collectSubjects(groups: { subjects: Element[] }[][]): Element[] {
    const map = new Map<string, Element>();
    for (const g of groups) for (const item of g) for (const s of item.subjects) if (!map.has(s.short) || (!map.get(s.short)!.long && s.long)) map.set(s.short, { short: s.short, long: s.long });
    return [...map.values()].sort((a, b) => a.short.localeCompare(b.short));
  }
}
