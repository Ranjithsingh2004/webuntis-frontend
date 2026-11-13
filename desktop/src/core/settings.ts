import type { ApiClient } from "./api/client";
import type { SettingsDocument } from "./api/types";
import { Signal, type ReadSignal, type Unsubscribe } from "./reactive";

type Path = string; // dot separated, e.g. "ui.theme.accent"

/** A path segment that would reach the prototype rather than a setting. */
const FORBIDDEN_SEGMENTS = new Set(["__proto__", "constructor", "prototype"]);

/** Settings paths are dot separated and must stay inside the document. */
function isSafePath(path: Path): boolean {
  return path.split(".").every((key) => key !== "" && !FORBIDDEN_SEGMENTS.has(key));
}

export function getPath(doc: unknown, path: Path): unknown {
  if (!isSafePath(path)) return undefined;
  let cur = doc;
  for (const key of path.split(".")) {
    if (cur === null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
}

/** Builds the nested merge patch {a: {b: value}} for "a.b". */
export function patchFor(path: Path, value: unknown): SettingsDocument {
  if (!isSafePath(path)) return {};
  const keys = path.split(".");
  const root: SettingsDocument = {};
  let cur = root;
  keys.forEach((k, i) => {
    if (i === keys.length - 1) cur[k] = value;
    else cur = cur[k] = {} as SettingsDocument;
  });
  return root;
}

function setPath(doc: SettingsDocument, path: Path, value: unknown): SettingsDocument {
  if (!isSafePath(path)) return doc;
  const out = structuredClone(doc);
  const keys = path.split(".");
  let cur = out as Record<string, unknown>;
  keys.slice(0, -1).forEach((k) => {
    if (typeof cur[k] !== "object" || cur[k] === null) cur[k] = {};
    cur = cur[k] as Record<string, unknown>;
  });
  const last = keys[keys.length - 1];
  if (value === null || value === undefined) delete cur[last];
  else cur[last] = value;
  return out;
}

/**
 * The patch that turns `cur` into `next`: keys `next` no longer has become
 * null, because merging an object into another unions their keys and nothing
 * else ever removes one. Keys are taken literally, so a subject code with a
 * dot in it (`D.LK`) stays one key instead of becoming a nested path.
 */
export function diffObject(cur: unknown, next: Record<string, unknown>): SettingsDocument {
  const isObj = (x: unknown): x is Record<string, unknown> => !!x && typeof x === "object" && !Array.isArray(x);
  const before = isObj(cur) ? cur : {};
  const out: SettingsDocument = {};
  for (const k of Object.keys(before)) {
    if (!Object.prototype.hasOwnProperty.call(next, k)) out[k] = null;
  }
  for (const [k, v] of Object.entries(next)) {
    out[k] = isObj(v) && isObj(before[k]) ? diffObject(before[k], v) : v;
  }
  return out;
}

/** Merges merge patches; unlike applying them, null values are kept so
 * the backend still sees the deletion. */
function combinePatches(a: SettingsDocument, b: SettingsDocument): SettingsDocument {
  const out = structuredClone(a);
  for (const [k, v] of Object.entries(b)) {
    const cur = out[k];
    const isObj = (x: unknown) => !!x && typeof x === "object" && !Array.isArray(x);
    out[k] = isObj(v) && isObj(cur) ? combinePatches(cur as SettingsDocument, v as SettingsDocument) : v;
  }
  return out;
}

/**
 * Client-side view of the settings document stored by the backend.
 *
 * Writes are applied locally right away (so the UI reacts instantly) and
 * sent to the backend as merge patches, batched over a short delay.
 * Defaults are registered by the settings schemas; get() falls back to
 * them for every missing value.
 */
export class SettingsStore {
  private readonly doc = new Signal<SettingsDocument>({});
  private readonly defaults = new Map<Path, unknown>();
  private pending: SettingsDocument = {};
  private timer: ReturnType<typeof setTimeout> | undefined;
  private flushing: Promise<void> = Promise.resolve();

  constructor(private readonly api: ApiClient) {}

  async load(): Promise<void> {
    this.doc.set(await this.api.getSettings());
  }

  /** The raw document (without defaults). */
  get document(): ReadSignal<SettingsDocument> {
    return this.doc;
  }

  registerDefault(path: Path, value: unknown): void {
    this.defaults.set(path, value);
  }

  defaultOf(path: Path): unknown {
    return this.defaults.get(path);
  }

  /** Whether the user set a value (instead of using the default). */
  isSet(path: Path): boolean {
    return getPath(this.doc.get(), path) !== undefined;
  }

  get<T>(path: Path, fallback?: T): T {
    const v = getPath(this.doc.get(), path);
    if (v !== undefined) return v as T;
    if (this.defaults.has(path)) return this.defaults.get(path) as T;
    return fallback as T;
  }

  /** Calls fn with the value of path now and whenever it changes. */
  watch<T>(path: Path, fn: (value: T) => void, fallback?: T): Unsubscribe {
    let last: string | undefined;
    return this.doc.subscribe(() => {
      const v = this.get<T>(path, fallback);
      const key = JSON.stringify(v);
      if (key === last) return;
      last = key;
      fn(v);
    });
  }

  /** Sets a value; undefined or null resets it to its default. */
  set(path: Path, value: unknown): void {
    this.doc.set(setPath(this.doc.get(), path, value));
    this.queue(patchFor(path, value ?? null));
  }

  /**
   * Replaces the object stored at path, deleting the keys it lost.
   *
   * Unlike set(), an empty value is kept as an empty object rather than
   * resetting the setting, and keys are compared literally: this is what
   * per-subject overrides need, since their keys are subject codes and a code
   * like `D.LK` cannot be addressed as a dotted path.
   */
  setObject(path: Path, value: Record<string, unknown>): void {
    if (!isSafePath(path)) return;
    const patch = diffObject(getPath(this.doc.get(), path), value);
    this.doc.set(setPath(this.doc.get(), path, value));
    this.queue(patchFor(path, patch));
  }

  private queue(patch: SettingsDocument): void {
    this.pending = combinePatches(this.pending, patch);
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.flush(), 250);
  }

  reset(path: Path): void {
    this.set(path, null);
  }

  /** Sends pending changes to the backend. */
  flush(): Promise<void> {
    clearTimeout(this.timer);
    const patch = this.pending;
    this.pending = {};
    if (Object.keys(patch).length === 0) return this.flushing;
    this.flushing = this.flushing.then(() => this.api.patchSettings(patch).then(() => undefined));
    return this.flushing;
  }

  async replace(doc: SettingsDocument): Promise<void> {
    await this.flush();
    this.doc.set(await this.api.replaceSettings(doc));
  }

  async resetAll(): Promise<void> {
    await this.flush();
    this.doc.set(await this.api.resetSettings());
  }

  /** Reloads after a change made elsewhere (e.g. another window). */
  async reload(): Promise<void> {
    if (Object.keys(this.pending).length === 0) await this.load();
  }
}
