import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/core/api/client";
import { defineModule } from "@/core/module";
import { settle, testContext } from "@/test/ctx";
import { snapshot } from "@/test/fixtures";
import { mountShell } from "./shell";

const page = (id: string, order: number, nav = true) =>
  defineModule({ id, title: id.toUpperCase(), icon: "info", order, route: { path: `/${id}`, nav, render: () => Object.assign(document.createElement("div"), { className: `view-${id}` }) } });

const broken = defineModule({ id: "broken", title: "Broken", icon: "info", route: { path: "/broken", render: () => { throw new Error("boom"); } } });

beforeEach(() => history.replaceState(null, "", "#/b"));
const hashchange = () => new Promise((r) => window.addEventListener("hashchange", r, { once: true }));

describe("shell", () => {
  it("builds the navigation from the registry and renders the current route", async () => {
    const ctx = await testContext(snapshot(), [page("b", 2), page("a", 1), page("hidden", 3, false)]);
    const root = document.createElement("div");
    mountShell(root, ctx);
    expect([...root.querySelectorAll(".nav-item")].map((a) => a.textContent)).toEqual(["A", "B"]);
    expect(root.querySelector(".view-b")).not.toBeNull();
    expect(root.querySelector(".nav-item.active")?.textContent).toBe("B");
    expect(root.querySelector(".topbar-title")?.textContent).toBe("B");
  });

  it("does not re-render the view when only the query changes", async () => {
    const render = vi.fn(() => Object.assign(document.createElement("div"), { className: "view-b" }));
    const ctx = await testContext(snapshot(), [defineModule({ id: "b", title: "B", icon: "info", route: { path: "/b", render } })]);
    const root = document.createElement("div");
    mountShell(root, ctx);
    const view = root.querySelector(".view-b");
    expect(render).toHaveBeenCalledTimes(1);

    ctx.router.setParams({ q: "math" });
    await settle();
    expect(render).toHaveBeenCalledTimes(1);
    expect(root.querySelector(".view-b")).toBe(view);
    expect(location.hash).toBe("#/b?q=math");
  });

  it("redirects unknown routes to the first page", async () => {
    history.replaceState(null, "", "#/nope");
    const ctx = await testContext(snapshot(), [page("a", 1)]);
    const root = document.createElement("div");
    const changed = hashchange();
    mountShell(root, ctx);
    await changed;
    expect(location.hash).toBe("#/a");
  });

  it("contains view errors and shows the error banner", async () => {
    history.replaceState(null, "", "#/broken");
    const ctx = await testContext(snapshot(), [broken]);
    const root = document.createElement("div");
    const origError = console.error;
    console.error = () => {};
    mountShell(root, ctx);
    console.error = origError;
    expect(root.querySelector(".content")?.textContent).toContain("boom");

    (ctx.api.overview as unknown as { mockRejectedValueOnce(e: unknown): void }).mockRejectedValueOnce(new ApiError("login", "bad password", 403));
    await ctx.data.loadOverview();
    await settle();
    expect(root.querySelector(".banner")?.textContent).toContain("WebUntis rejected the login");
  });

  it("collapses the sidebar through the settings", async () => {
    const ctx = await testContext(snapshot(), [page("b", 1)]);
    const root = document.createElement("div");
    mountShell(root, ctx);
    (root.querySelector(".nav-collapse") as HTMLButtonElement).click();
    expect(root.querySelector(".sidebar")?.classList.contains("collapsed")).toBe(true);
  });

  it("shows toasts", async () => {
    const ctx = await testContext(snapshot(), [page("b", 1)]);
    const root = document.createElement("div");
    mountShell(root, ctx);
    ctx.toasts.show("success", "Saved");
    expect(root.querySelector(".toast-success")?.textContent).toContain("Saved");
  });
});
