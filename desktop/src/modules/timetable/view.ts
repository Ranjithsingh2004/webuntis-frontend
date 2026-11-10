import type { AppContext } from "@/core/app";
import type { Holiday, Lesson, Snapshot } from "@/core/api/types";
import { addDays, isWeekend, isoWeek, minutes, nowMinutes, range, startOfWeek, today } from "@/core/dates";
import { h, replace } from "@/core/dom";
import { icon } from "@/core/icons";
import type { Scope } from "@/core/reactive";
import { showLessonDetails, statusText } from "@/ui/lesson";
import { assignColumns, clipGrid, effectiveGrid, linearAxis, mergeAdjacent, periodAxis, type Axis } from "./layout";

type Mode = "week" | "day";

/** Reads a timetable setting. */
function opt<T>(ctx: AppContext, key: string): T {
  return ctx.settings.get<T>(`timetable.${key}`);
}

export function renderTimetable(ctx: AppContext, scope: Scope, params: URLSearchParams): HTMLElement {
  const t = ctx.i18n.t.bind(ctx.i18n);
  let mode: Mode = (params.get("mode") as Mode) || opt<Mode>(ctx, "defaultMode");
  let anchor = params.get("date") || today();

  const label = h("div", { class: "tt-label" });
  const grid = h("div", { class: "tt-grid-wrap" });
  const modeSwitch = h(
    "div",
    { class: "segmented" },
    (["week", "day"] as Mode[]).map((m) =>
      h("button", { type: "button", dataset: { mode: m }, onclick: () => go(anchor, m) }, t(m === "week" ? "Week" : "Day")),
    ),
  );
  const dateInput = h("input", { type: "date", class: "input", attrs: { "aria-label": t("Go to date") } });
  dateInput.addEventListener("change", () => dateInput.value && go(dateInput.value, mode));

  const step = (dir: number) => go(addDays(anchor, dir * (mode === "week" ? 7 : 1)), mode);
  const toolbar = h(
    "div",
    { class: "tt-toolbar" },
    h("button", { class: "icon-btn", title: t("Previous"), attrs: { "aria-label": t("Previous") }, onclick: () => step(-1) }, icon("left")),
    h("button", { class: "btn", onclick: () => go(today(), mode) }, t("Today")),
    h("button", { class: "icon-btn", title: t("Next"), attrs: { "aria-label": t("Next") }, onclick: () => step(1) }, icon("right")),
    label,
    h("div", { class: "spacer" }),
    dateInput,
    modeSwitch,
  );

  let token = 0;
  async function load(): Promise<void> {
    const my = ++token;
    const days = visibleDays(ctx, anchor, mode, null);
    const from = mode === "week" ? startOfWeek(anchor, ctx.i18n.firstDay()) : anchor;
    const to = mode === "week" ? addDays(from, 6) : anchor;
    label.replaceChildren(rangeLabel(ctx, days, mode));
    dateInput.value = anchor;
    for (const b of modeSwitch.querySelectorAll("button")) b.classList.toggle("active", b.dataset.mode === mode);
    grid.classList.add("loading");
    try {
      const snap = await ctx.data.window(from, to);
      if (my !== token) return;
      draw(snap);
    } catch (err) {
      if (my !== token) return;
      replace(grid, h("div", { class: "empty" }, t("Could not load the timetable: {error}", { error: (err as Error).message })));
    } finally {
      if (my === token) grid.classList.remove("loading");
    }
  }

  let drawScope = scope.child();
  let last: Snapshot | null = null;
  function draw(snap: Snapshot): void {
    last = snap;
    drawScope.dispose();
    drawScope = scope.child();
    replace(grid, buildGrid(ctx, drawScope, snap, visibleDays(ctx, anchor, mode, snap), mode));
  }

  function go(date: string, m: Mode): void {
    anchor = date;
    mode = m;
    ctx.router.setParams({ date: date === today() ? null : date, mode: m === opt<Mode>(ctx, "defaultMode") ? null : m });
    void load();
  }

  // Redraw on setting changes (no refetch), reload when data is refreshed.
  let settingsKey = "";
  scope.watch(ctx.settings.document, () => {
    const key = JSON.stringify([ctx.settings.get("ui"), ctx.settings.get("timetable"), ctx.settings.get("ui.names"), ctx.settings.get("subjects"), ctx.settings.get("filters")]);
    if (key === settingsKey) return;
    const first = settingsKey === "";
    settingsKey = key;
    if (!first && last) draw(last);
  });
  scope.watch(ctx.data.revision, () => void load(), false);
  // The shell only remounts on path changes, so follow query changes here:
  // history navigation, or a step the toolbar did not trigger itself.
  scope.watch(
    ctx.router.route,
    (r) => {
      if (r.path !== "/timetable") return;
      const nextMode = (r.params.get("mode") as Mode) || opt<Mode>(ctx, "defaultMode");
      const nextAnchor = r.params.get("date") || today();
      if (nextMode === mode && nextAnchor === anchor) return;
      mode = nextMode;
      anchor = nextAnchor;
      void load();
    },
    false,
  );
  // Keyboard commands (see the timetable module) arrive through the bus.
  scope.add(ctx.bus.on("timetable:go", (dir) => (dir === 0 ? go(today(), mode) : step(dir))));
  scope.add(ctx.bus.on("timetable:mode", (m) => go(anchor, m)));
  void load();

  return h("div", { class: "page tt-page" }, toolbar, grid);
}

/** The days shown for the anchor date, honouring the weekend setting. */
function visibleDays(ctx: AppContext, anchor: string, mode: Mode, snap: Snapshot | null): string[] {
  if (mode === "day") return [anchor];
  const start = startOfWeek(anchor, ctx.i18n.firstDay());
  const all = range(start, addDays(start, 6));
  const weekends = opt<string>(ctx, "weekends");
  if (weekends === "always") return all;
  const hasWeekendLessons = !!snap?.lessons.some((l) => isWeekend(l.date));
  return all.filter((d) => !isWeekend(d) || (weekends === "auto" && hasWeekendLessons));
}

function rangeLabel(ctx: AppContext, days: string[], mode: Mode): HTMLElement {
  const i = ctx.i18n;
  const text = mode === "day" ? i.date(days[0], "long") : `${i.date(days[0], "dayMonth")} – ${i.date(days[days.length - 1], "dayMonth")}`;
  return h("span", null, h("strong", null, text), h("span", { class: "muted small" }, `  ${i.t("Week {n}", { n: isoWeek(days[0]) })}`));
}

function holidayOn(day: string, holidays: Holiday[]): Holiday | undefined {
  return holidays.find((hd) => hd.start <= day && day <= hd.end);
}

function filterLessons(ctx: AppContext, lessons: Lesson[]): Lesson[] {
  let out = lessons;
  if (opt<boolean>(ctx, "hideCancelled")) out = out.filter((l) => !l.cancelled);
  if (opt<boolean>(ctx, "hideRemoved")) out = out.filter((l) => !l.removed);
  if (opt<boolean>(ctx, "mergeAdjacent")) out = mergeAdjacent(out);
  return out;
}

function buildAxis(ctx: AppContext, snap: Snapshot, lessons: Lesson[]): Axis {
  if (opt<string>(ctx, "layout") === "proportional") {
    const times = lessons.flatMap((l) => [minutes(l.start), minutes(l.end)]);
    const gridTimes = snap.timeGrid.flatMap((p) => [minutes(p.start), minutes(p.end)]);
    const all = [...times, ...gridTimes];
    const start = all.length ? Math.min(...all) : 8 * 60;
    const end = all.length ? Math.max(...all) : 16 * 60;
    return linearAxis(start, end, opt<number>(ctx, "pxPerMinute"));
  }
  const g = clipGrid(effectiveGrid(snap.timeGrid, lessons), opt<number>(ctx, "firstPeriod"), opt<number>(ctx, "lastPeriod"));
  return periodAxis(g, opt<number>(ctx, "rowHeight"), opt<number>(ctx, "breakHeight"));
}

function buildGrid(ctx: AppContext, scope: Scope, snap: Snapshot, days: string[], mode: Mode): HTMLElement {
  const lessons = filterLessons(ctx, ctx.display.visibleLessons(snap.lessons));
  const axis = buildAxis(ctx, snap, lessons);
  const now = today();

  if (!lessons.length && !days.some((d) => holidayOn(d, snap.holidays))) {
    return h("div", { class: "card empty" }, ctx.i18n.t("No lessons in this period."));
  }

  const timeCol = h(
    "div",
    { class: "tt-times", style: { height: `${axis.height}px` } },
    axis.marks.map((m) =>
      h(
        "div",
        { class: ["tt-mark", m.kind], style: { top: `${m.top}px`, height: `${m.height}px` } },
        opt<boolean>(ctx, "showPeriodNumbers") || m.kind === "hour" ? h("span", { class: "tt-mark-label" }, m.kind === "hour" ? ctx.i18n.time(m.label) : m.label) : null,
        m.sub && opt<boolean>(ctx, "showPeriodTimes") ? h("span", { class: "tt-mark-sub" }, m.sub.split("\n").map((x) => ctx.i18n.time(x)).join("\n")) : null,
      ),
    ),
  );

  const columns = days.map((day) => {
    const isToday = day === now && opt<boolean>(ctx, "highlightToday");
    const head = h(
      "div",
      { class: ["tt-dayhead", isToday && "today"] },
      h("span", { class: "tt-dow" }, ctx.i18n.weekday(day, mode === "day" ? "long" : "short")),
      h("span", { class: "tt-date" }, ctx.i18n.shortNumeric(day)),
    );
    const body = h("div", { class: ["tt-day", isToday && "today"], style: { height: `${axis.height}px` } });
    for (const m of axis.marks) body.appendChild(h("div", { class: "tt-rowline", style: { top: `${m.top}px`, height: `${m.height}px` } }));

    const holiday = holidayOn(day, snap.holidays);
    if (holiday && opt<boolean>(ctx, "showHolidays")) body.appendChild(h("div", { class: "tt-holiday" }, icon("sun", 20), holiday.name));

    for (const p of assignColumns(lessons.filter((l) => l.date === day))) {
      const top = axis.y(minutes(p.item.start));
      const height = Math.max(18, axis.y(minutes(p.item.end)) - top - 3);
      body.appendChild(lessonCell(ctx, p.item, { top, height, left: p.col / p.cols, width: 1 / p.cols }, day === now));
    }

    if (day === now && opt<boolean>(ctx, "nowLine")) {
      const line = h("div", { class: "tt-now" });
      const place = () => {
        const t = nowMinutes();
        const y = axis.y(t);
        line.style.top = `${y}px`;
        line.style.display = y <= 0 || y >= axis.height ? "none" : "";
        if (opt<boolean>(ctx, "dimPast")) {
          for (const c of body.querySelectorAll<HTMLElement>(".tt-lesson")) c.classList.toggle("past", minutes(c.dataset.end!) <= t);
        }
      };
      place();
      scope.interval(place, 30_000);
      body.appendChild(line);
    }
    return h("div", { class: "tt-col" }, head, body);
  });

  return h(
    "div",
    { class: ["tt-grid", opt<boolean>(ctx, "compact") && "compact"], style: { "--tt-font": String(opt<number>(ctx, "fontScale")) } },
    h("div", { class: "tt-col tt-time-col" }, h("div", { class: "tt-dayhead" }), timeCol),
    columns,
  );
}

function lessonCell(
  ctx: AppContext,
  l: Lesson,
  box: { top: number; height: number; left: number; width: number },
  isToday: boolean,
): HTMLElement {
  const teacher = opt<boolean>(ctx, "showTeacher") ? ctx.display.teachers(l.teachers) : "";
  const room = opt<boolean>(ctx, "showRoom") ? ctx.display.rooms(l.rooms) : "";
  const label = opt<boolean>(ctx, "showStatusLabel") && statusText[l.status] ? ctx.i18n.t(statusText[l.status]) : "";
  const changedTeacher = l.teachers.some((t) => t.replaces);
  const changedRoom = l.rooms.some((r) => r.replaces);
  const custom = ctx.display.subjectStyle(l);
  const el = h(
    "button",
    {
      type: "button",
      class: ["tt-lesson", "status-box", `st-${l.status}`, (l.cancelled || l.removed) && "struck"],
      style: {
        "--c": custom.color ?? `var(--c-${l.status})`,
        top: `${box.top}px`,
        height: `${box.height}px`,
        left: `calc(${box.left * 100}% + 2px)`,
        width: `calc(${box.width * 100}% - 4px)`,
      },
      dataset: { end: l.end },
      title: `${ctx.display.subject(l)} ${ctx.i18n.timeRange(l.start, l.end)}`,
      onclick: () => showLessonDetails(ctx, l),
    },
    h("strong", { class: "tt-subject" }, custom.emoji ? `${custom.emoji} ` : "", ctx.display.subject(l)),
    room ? h("span", { class: ["tt-meta", changedRoom && "changed"] }, room) : null,
    teacher ? h("span", { class: ["tt-meta", changedTeacher && "changed"] }, teacher) : null,
    opt<boolean>(ctx, "showTime") ? h("span", { class: "tt-meta muted" }, ctx.i18n.timeRange(l.start, l.end)) : null,
    label ? h("span", { class: "tt-badge" }, label) : null,
  );
  if (isToday && opt<boolean>(ctx, "dimPast") && minutes(l.end) <= nowMinutes()) el.classList.add("past");
  return el;
}
