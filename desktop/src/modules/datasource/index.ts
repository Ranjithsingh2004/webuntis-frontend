import { defineModule } from "@/core/module";
import type { SettingsStore } from "@/core/settings";

const isCLI = (s: SettingsStore) => s.get("backend.provider") === "cli";
const isSample = (s: SettingsStore) => !isCLI(s);

/**
 * Where the data comes from. These settings live under "backend" and are
 * read by untisd itself, which switches providers as soon as they change.
 * The defaults mirror backend/internal/settings.DefaultBackend.
 */
export default defineModule({
  id: "datasource",
  title: "Data source",
  icon: "database",
  settings: [
    {
      id: "datasource",
      title: "Data source",
      icon: "database",
      order: 5,
      description:
        "Sample data is fully fictional and needs no account. The untis CLI shows your real timetable; set it up once with `untis init` in a terminal.",
      fields: [
        {
          key: "backend.provider",
          type: "segmented",
          label: "Source",
          default: "sample",
          keywords: ["provider", "demo", "real"],
          options: [
            { value: "sample", label: "Sample data" },
            { value: "cli", label: "untis CLI" },
          ],
        },
        {
          key: "backend.refreshMinutes",
          type: "number",
          label: "Refresh every",
          description: "Background refresh interval. 0 turns automatic refreshes off.",
          default: 10,
          min: 0,
          max: 1440,
          unit: "min",
          keywords: ["interval", "poll", "update"],
        },
        {
          key: "backend.daysBack",
          type: "number",
          label: "History",
          description: "How far back exams, homework and absences are loaded.",
          default: 30,
          min: 0,
          max: 366,
          unit: "days",
        },
        {
          key: "backend.daysAhead",
          type: "number",
          label: "Look ahead",
          description: "How far ahead the dashboard and lists look.",
          default: 60,
          min: 1,
          max: 366,
          unit: "days",
        },
        {
          key: "backend.detectChanges",
          type: "toggle",
          label: "Detect changes",
          description: "Compare each refresh with the previous one and report cancellations, substitutions and more.",
          default: true,
          keywords: ["notifications", "diff"],
        },
        {
          key: "backend.sample.latencyMs",
          type: "range",
          label: "Simulated latency",
          description: "Delay sample answers to see loading states.",
          default: 0,
          min: 0,
          max: 3000,
          step: 100,
          unit: " ms",
          visibleIf: isSample,
        },
        {
          key: "backend.cli.binary",
          type: "text",
          label: "untis executable",
          description: "Name in PATH or absolute path.",
          default: "untis",
          monospace: true,
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.transport",
          type: "select",
          label: "Transport",
          description: "How the CLI talks to WebUntis (--transport).",
          default: "auto",
          options: [
            { value: "auto", label: "Auto (HTTP, browser as fallback)" },
            { value: "http", label: "HTTP only" },
            { value: "browser", label: "Browser (Playwright)" },
          ],
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.maxAge",
          type: "text",
          label: "Use CLI cache up to",
          description: "Passed as --max-age, e.g. 5m or 1h. Empty always fetches. Manual refreshes always fetch.",
          default: "5m",
          monospace: true,
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.offline",
          type: "toggle",
          label: "Offline mode",
          description: "Answer only from the CLI cache and never touch the network (--offline).",
          default: false,
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.configPath",
          type: "text",
          label: "Config file",
          description: "Optional --config path. Empty uses the CLI default.",
          default: "",
          placeholder: "~/.config/untis/config.json",
          monospace: true,
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.envPath",
          type: "text",
          label: "Credentials file",
          description: "Optional --env path. Empty uses the CLI default.",
          default: "",
          placeholder: "~/.config/untis/.env",
          monospace: true,
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.extraArgs",
          type: "list",
          label: "Extra arguments",
          description: "Appended to the snapshot and lookup calls, for CLI options without a field here. Press Enter to add.",
          default: [],
          placeholder: "--long",
          visibleIf: isCLI,
        },
        {
          key: "backend.cli.timeoutSec",
          type: "number",
          label: "Timeout",
          description: "Give up on a CLI call after this long.",
          default: 120,
          min: 10,
          max: 600,
          unit: "s",
          visibleIf: isCLI,
        },
      ],
    },
  ],
});
