import type { AppContext } from "@/core/app";
import { Display, autoColor, type SubjectOverride } from "@/core/display";
import { h, replace } from "@/core/dom";
import { icon } from "@/core/icons";
import type { Scope } from "@/core/reactive";

/**
 * Applies one change to the per-subject override map.
 *
 * The map is written whole, keyed by the subject code, rather than as the
 * dotted path `subjects.custom.<code>`: WebUntis codes such as `D.LK` or
 * `M.g1` contain dots, and a dotted path splits them into nested objects that
 * then never match the code again.
 */
export function applySubjectOverride(
  all: Record<string, SubjectOverride>,
  code: string,
  patch: Partial<SubjectOverride>,
): Record<string, SubjectOverride> {
  const map = { ...all };
  const next: SubjectOverride = { ...map[code], ...patch };
  for (const k of Object.keys(next) as (keyof SubjectOverride)[]) {
    if (next[k] === "" || next[k] === false || next[k] === undefined) delete next[k];
  }
  if (Object.keys(next).length) map[code] = next;
  else delete map[code];
  return map;
}

/** Removes every override for one subject code. */
export function removeSubjectOverride(
  all: Record<string, SubjectOverride>,
  code: string,
): Record<string, SubjectOverride> {
  const map = { ...all };
  delete map[code];
  return map;
}

/** One row per subject found in the data: rename, colour, emoji, hide. */
export function subjectEditor(ctx: AppContext, scope: Scope): HTMLElement {
  const table = h("div", { class: "subject-editor" });
  const all = () => ctx.settings.get<Record<string, SubjectOverride>>("subjects.custom", {});
  // Always store the map itself, empty included: it is one setting, and an
  // empty object keeps `get("subjects.custom")` returning a map to render.
  const save = (map: Record<string, SubjectOverride>) => ctx.settings.set("subjects.custom", map);
  const update = (code: string, patch: Partial<SubjectOverride>) => save(applySubjectOverride(all(), code, patch));

  let structure = "";
  const render = () => {
    const snap = ctx.data.overview.get();
    const subjects = snap ? Display.collectSubjects([snap.lessons, snap.exams, snap.homework]) : [];
    // Rebuild only when the subject list changes, so inputs keep focus.
    const key = subjects.map((s) => s.short).join();
    if (key === structure) return;
    structure = key;
    if (!subjects.length) return replace(table, h("div", { class: "muted small" }, "Subjects appear here once data is loaded."));
    replace(
      table,
      subjects.map((s) => {
        const o = all()[s.short] ?? {};
        const name = h("input", { type: "text", class: "input", placeholder: s.long ?? s.short, value: o.name ?? "", attrs: { "aria-label": `Name for ${s.short}` } });
        name.addEventListener("change", () => update(s.short, { name: name.value.trim() }));
        const color = h("input", { type: "color", class: "color", value: o.color ?? toHex(autoColor(s.short)), attrs: { "aria-label": `Colour for ${s.short}` } });
        color.addEventListener("input", () => update(s.short, { color: color.value }));
        const emoji = h("input", { type: "text", class: "input emoji", maxLength: 4, placeholder: "🙂", value: o.emoji ?? "", attrs: { "aria-label": `Emoji for ${s.short}` } });
        emoji.addEventListener("change", () => update(s.short, { emoji: emoji.value.trim() }));
        const hidden = h("input", { type: "checkbox", class: "switch", checked: !!o.hidden, title: "Hide everywhere", attrs: { "aria-label": `Hide ${s.short}` } });
        hidden.addEventListener("change", () => update(s.short, { hidden: hidden.checked }));
        const reset = h("button", { type: "button", class: "icon-btn", title: "Reset this subject", onclick: () => { save(removeSubjectOverride(all(), s.short)); structure = ""; render(); } }, icon("undo", 14));
        return h("div", { class: "subject-row", dataset: { code: s.short } }, h("div", null, h("strong", null, s.short), h("div", { class: "muted small" }, s.long ?? "")), name, color, emoji, h("label", { class: "row-inline small muted" }, hidden, "hide"), reset);
      }),
    );
  };
  scope.watch(ctx.data.overview, render);
  return table;
}

/** <input type=color> needs #rrggbb; convert the hsl() auto colour. */
export function toHex(color: string): string {
  const m = /hsl\((\d+)\s+(\d+)%\s+(\d+)%\)/.exec(color);
  if (!m) return color;
  const [hue, s, l] = [Number(m[1]), Number(m[2]) / 100, Number(m[3]) / 100];
  const f = (n: number) => {
    const k = (n + hue / 30) % 12;
    const c = l - s * Math.min(l, 1 - l) * Math.max(-1, Math.min(k - 3, 9 - k, 1));
    return Math.round(c * 255).toString(16).padStart(2, "0");
  };
  return `#${f(0)}${f(8)}${f(4)}`;
}
